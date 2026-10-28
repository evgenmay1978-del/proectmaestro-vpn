package com.maestrovpn.tv.utils

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.util.concurrent.TimeUnit

/**
 * Minimal SOCKS5 client for the connectivity probe.
 *
 * The app never rides its own tunnel — PackagePolicy keeps the app package outside the VPN on
 * purpose, so the control plane (claim, /sub, OTA) survives a dead tunnel (incident 21.10.2026).
 * The price: a plain HTTPS request from the app proves nothing about the VPN, it travels the
 * direct route. The only way to ask "does traffic actually flow through the tunnel" is to relay
 * the probe through a SOCKS5 listener that lives inside it:
 *
 *  - the CDN helper process (libmaestro_xhttp.so) exposes one, with credentials from
 *    WhiteListConfig.payload;
 *  - the service can attach its own probe inbound to the applied config.
 *
 * Pure java.net, no Android: the JVM unit tests drive it against a fake SOCKS5 server.
 */
internal object Socks5Tunnel {
    private const val VERSION = 0x05
    private const val METHOD_NO_AUTH = 0x00
    private const val METHOD_USER_PASS = 0x02
    private const val METHOD_NONE_ACCEPTABLE = 0xFF
    private const val AUTH_VERSION = 0x01
    private const val AUTH_OK = 0x00
    private const val CMD_CONNECT = 0x01
    private const val RESERVED = 0x00
    private const val ATYP_IPV4 = 0x01
    private const val ATYP_DOMAIN = 0x03
    private const val ATYP_IPV6 = 0x04
    private const val REP_SUCCESS = 0x00
    private const val MAX_HOST_BYTES = 255
    private const val MAX_CREDENTIAL_BYTES = 255

    /** Only used for the CDN helper process; the probe inbound has no authentication. */
    data class Credentials(val username: String, val password: String) {
        override fun toString(): String = "Credentials(redacted)"
    }

    /** Where the handshake stopped. Logged as a short token; carries no host and no credential. */
    enum class Failure { CONNECT, GREETING, AUTH, REQUEST, REPLY, TIMEOUT }

    private class HandshakeFailure(val reason: Failure) : Exception()

    /**
     * Opens a TCP stream to host:port through the SOCKS5 endpoint on 127.0.0.1:proxyPort.
     *
     * Returns null on any failure — the caller must treat "no socket" as "the tunnel does not
     * carry traffic right now", never as a success. onFailure receives the stage for the log.
     * The whole handshake honours one timeoutMs deadline.
     */
    fun open(
        proxyPort: Int,
        credentials: Credentials?,
        host: String,
        port: Int,
        timeoutMs: Int,
        onFailure: (Failure) -> Unit = {},
    ): Socket? {
        if (proxyPort !in 1..65535 || port !in 1..65535 || host.isEmpty() || timeoutMs <= 0) {
            onFailure(Failure.REQUEST)
            return null
        }
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMs.toLong())
        val socket = Socket()
        var failure = Failure.CONNECT
        try {
            socket.tcpNoDelay = true
            socket.connect(InetSocketAddress(InetAddress.getLoopbackAddress(), proxyPort), remainingMillis(deadline))
            socket.soTimeout = remainingMillis(deadline)
            val output = socket.getOutputStream()
            val input = socket.getInputStream()
            val method = greeting(input, output, credentials, deadline)
            if (method == METHOD_USER_PASS) {
                // The greeting helper never authenticates: doing it twice would hang on the
                // server's silence and report a healthy tunnel as broken.
                failure = Failure.AUTH
                authenticate(input, output, credentials, deadline)
            }
            failure = Failure.REQUEST
            connect(input, output, host, port, deadline)
            return socket
        } catch (e: HandshakeFailure) {
            onFailure(e.reason)
        } catch (_: IOException) {
            onFailure(failure)
        } catch (_: IllegalArgumentException) {
            onFailure(failure)
        }
        closeQuietly(socket)
        return null
    }

    private fun greeting(
        input: InputStream,
        output: java.io.OutputStream,
        credentials: Credentials?,
        deadline: Long,
    ): Int {
        val methods = if (credentials == null) byteArrayOf(METHOD_NO_AUTH.toByte()) else
            byteArrayOf(METHOD_NO_AUTH.toByte(), METHOD_USER_PASS.toByte())
        val request = ByteArray(2 + methods.size)
        request[0] = VERSION.toByte()
        request[1] = methods.size.toByte()
        methods.copyInto(request, 2)
        output.write(request)
        output.flush()
        val reply = readExactly(input, 2, deadline) ?: handshake(Failure.GREETING)
        if (reply[0] != VERSION.toByte() || reply[1] == METHOD_NONE_ACCEPTABLE.toByte()) {
            handshake(Failure.GREETING)
        }
        val method = reply[1].toInt() and 0xff
        if (method == METHOD_USER_PASS && credentials == null) handshake(Failure.GREETING)
        return method
    }

    private fun authenticate(
        input: InputStream,
        output: java.io.OutputStream,
        credentials: Credentials?,
        deadline: Long,
    ) {
        if (credentials == null) return
        val user = credentials.username.toByteArray(Charsets.UTF_8)
        val pass = credentials.password.toByteArray(Charsets.UTF_8)
        if (user.isEmpty() || pass.isEmpty() || user.size > MAX_CREDENTIAL_BYTES || pass.size > MAX_CREDENTIAL_BYTES) {
            handshake(Failure.AUTH)
        }
        val request = ByteArray(3 + user.size + pass.size)
        request[0] = AUTH_VERSION.toByte()
        request[1] = user.size.toByte()
        user.copyInto(request, 2)
        request[2 + user.size] = pass.size.toByte()
        pass.copyInto(request, 3 + user.size)
        output.write(request)
        output.flush()
        val reply = readExactly(input, 2, deadline) ?: handshake(Failure.AUTH)
        if (reply[0] != AUTH_VERSION.toByte() || reply[1] != AUTH_OK.toByte()) handshake(Failure.AUTH)
    }

    private fun connect(
        input: InputStream,
        output: java.io.OutputStream,
        host: String,
        port: Int,
        deadline: Long,
    ) {
        val hostBytes = host.toByteArray(Charsets.US_ASCII)
        if (hostBytes.isEmpty() || hostBytes.size > MAX_HOST_BYTES) handshake(Failure.REQUEST)
        val request = ByteArrayOutputStream(7 + hostBytes.size)
        request.write(VERSION)
        request.write(CMD_CONNECT)
        request.write(RESERVED)
        request.write(ATYP_DOMAIN)
        request.write(hostBytes.size)
        request.write(hostBytes)
        request.write(port shr 8 and 0xFF)
        request.write(port and 0xFF)
        output.write(request.toByteArray())
        output.flush()
        val head = readExactly(input, 4, deadline) ?: handshake(Failure.REPLY)
        if (head[0] != VERSION.toByte()) handshake(Failure.REPLY)
        if (head[1] != REP_SUCCESS.toByte()) handshake(Failure.REPLY)
        val addressBytes = when (head[3].toInt() and 0xff) {
            ATYP_IPV4 -> 4
            ATYP_DOMAIN -> (readExactly(input, 1, deadline) ?: handshake(Failure.REPLY))[0].toInt() and 0xff
            ATYP_IPV6 -> 16
            else -> handshake(Failure.REPLY)
        }
        if (readExactly(input, addressBytes + 2, deadline) == null) handshake(Failure.REPLY)
    }

    private fun handshake(reason: Failure): Nothing = throw HandshakeFailure(reason)

    private fun remainingMillis(deadline: Long): Int {
        val left = TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime())
        if (left <= 0) handshake(Failure.TIMEOUT)
        return if (left > Int.MAX_VALUE) Int.MAX_VALUE else left.toInt()
    }

    private fun readExactly(input: InputStream, count: Int, deadline: Long): ByteArray? {
        val buffer = ByteArray(count)
        var read = 0
        while (read < count) {
            val n = try {
                input.read(buffer, read, count - read)
            } catch (_: java.net.SocketTimeoutException) {
                handshake(Failure.TIMEOUT)
            }
            if (n < 0) return null
            read += n
        }
        // Re-arm the timeout for the next stage from the same deadline.
        remainingMillis(deadline)
        return buffer
    }

    private fun closeQuietly(socket: Socket) {
        try {
            socket.close()
        } catch (_: IOException) {
        }
    }
}

package com.maestrovpn.tv.utils

import java.io.Closeable
import java.io.IOException
import java.io.InputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The probe is the only in-app evidence that traffic actually flows through the tunnel: the app
 * itself never rides the tunnel (PackagePolicy keeps it outside), so these tests drive the SOCKS5
 * client against a real fake server on loopback — a handshake that only looks right on paper would
 * make the service report a dead tunnel as connected.
 */
class TunnelProbeTest {
    private val target = TunnelProbeTarget("wapmixx.ru", 8911, "/healthz")

    @Test
    fun connectsThroughTheProxyAndRelaysBytes() {
        FakeSocks5().use { proxy ->
            val socket = Socks5Tunnel.open(proxy.port, null, target.host, target.port, 3_000)
            assertNotNull("handshake must succeed against a well-formed proxy", socket)
            socket!!.use { tunnel ->
                tunnel.soTimeout = 3_000
                tunnel.outputStream.write("ping".toByteArray())
                tunnel.outputStream.flush()
                val echo = ByteArray(4)
                readFully(tunnel.getInputStream(), echo)
                assertEquals("ping", String(echo))
            }
            assertEquals(target.host, proxy.requestedHost)
            assertEquals(target.port, proxy.requestedPort)
        }
    }

    @Test
    fun authenticatesWithCredentials() {
        FakeSocks5(requireAuth = true, expectedUser = "probe", expectedPassword = "s3cret").use { proxy ->
            val socket = Socks5Tunnel.open(
                proxy.port,
                Socks5Tunnel.Credentials("probe", "s3cret"),
                target.host,
                target.port,
                3_000,
            )
            assertNotNull(socket)
            socket?.close()
            assertEquals("probe", proxy.acceptedUser)
            assertEquals("s3cret", proxy.acceptedPassword)
        }
    }

    @Test
    fun reportsRefusedReply() {
        FakeSocks5(replyCode = 0x05).use { proxy ->
            var failure: Socks5Tunnel.Failure? = null
            val socket = Socks5Tunnel.open(proxy.port, null, target.host, target.port, 3_000) { failure = it }
            assertNull("a refused CONNECT is not a working tunnel", socket)
            assertEquals(Socks5Tunnel.Failure.REPLY, failure)
        }
    }

    @Test
    fun reportsAuthRejection() {
        FakeSocks5(requireAuth = true, expectedUser = "right", expectedPassword = "right").use { proxy ->
            var failure: Socks5Tunnel.Failure? = null
            val socket = Socks5Tunnel.open(
                proxy.port,
                Socks5Tunnel.Credentials("wrong", "wrong"),
                target.host,
                target.port,
                3_000,
            ) { failure = it }
            assertNull(socket)
            assertEquals(Socks5Tunnel.Failure.AUTH, failure)
        }
    }

    @Test
    fun reportsMissingListener() {
        val port = ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { it.localPort }
        var failure: Socks5Tunnel.Failure? = null
        val socket = Socks5Tunnel.open(port, null, target.host, target.port, 1_000) { failure = it }
        assertNull(socket)
        assertEquals(Socks5Tunnel.Failure.CONNECT, failure)
    }

    @Test
    fun reportsTimeoutWhenTheProxyNeverAnswers() {
        ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { silent ->
            var failure: Socks5Tunnel.Failure? = null
            val started = System.nanoTime()
            val socket = Socks5Tunnel.open(silent.localPort, null, target.host, target.port, 400) { failure = it }
            val elapsedMs = (System.nanoTime() - started) / 1_000_000
            assertNull(socket)
            assertEquals(Socks5Tunnel.Failure.TIMEOUT, failure)
            assertTrue("deadline must bound the handshake, took " + elapsedMs + " ms", elapsedMs < 3_000)
        }
    }

    @Test
    fun probeFailsWhenNothingListens() {
        val port = ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { it.localPort }
        val result = TunnelProbe.probe(port, null, target, 1_000)
        assertFalse(result.ok)
        assertEquals("connect", result.failure)
    }

    @Test
    fun probeFailsWhenTheCarriedStreamIsNotTls() {
        FakeSocks5().use { proxy ->
            val result = TunnelProbe.probe(proxy.port, null, target, 2_000)
            assertFalse("a relay that carries no TLS is not a working tunnel", result.ok)
            assertEquals("tls", result.failure)
            assertEquals(target.host, proxy.requestedHost)
        }
    }

    @Test
    fun panelTargetMatchesBackendUrl() {
        val panel = TunnelProbe.panelTarget()
        assertNotNull(panel)
        assertEquals(java.net.URL(com.maestrovpn.tv.BuildConfig.BACKEND_URL).host, panel!!.host)
        assertTrue(panel.path.startsWith("/"))
    }

    private fun readFully(input: InputStream, buffer: ByteArray) {
        var read = 0
        while (read < buffer.size) {
            val n = input.read(buffer, read, buffer.size - read)
            if (n < 0) throw IOException("eof after " + read + " bytes")
            read += n
        }
    }

    /**
     * A real SOCKS5 server on loopback. Deliberately speaks the wire protocol instead of mocking
     * the client: the test then fails when the client's byte layout is wrong, which is exactly the
     * defect that would make every probe report a broken tunnel.
     */
    private class FakeSocks5(
        private val requireAuth: Boolean = false,
        private val expectedUser: String? = null,
        private val expectedPassword: String? = null,
        private val replyCode: Int = 0x00,
    ) : Closeable {
        private val server = ServerSocket(0, 1, InetAddress.getLoopbackAddress())
        private val thread = Thread { serve() }.apply {
            isDaemon = true
            name = "fake-socks5"
        }

        val port: Int get() = server.localPort
        @Volatile var requestedHost: String? = null
        @Volatile var requestedPort: Int = -1
        @Volatile var acceptedUser: String? = null
        @Volatile var acceptedPassword: String? = null

        init {
            thread.start()
        }

        private fun serve() {
            try {
                server.accept().use { client ->
                    val input = client.getInputStream()
                    val output = client.getOutputStream()
                    if (input.read() != 0x05) return
                    val methodCount = input.read()
                    if (methodCount < 0) return
                    val methods = ByteArray(methodCount)
                    readFully(input, methods)
                    val method = if (requireAuth) 0x02 else 0x00
                    output.write(byteArrayOf(0x05, method.toByte()))
                    output.flush()
                    if (method == 0x02) {
                        if (input.read() != 0x01) return
                        val userLength = input.read()
                        val user = ByteArray(userLength)
                        readFully(input, user)
                        val passwordLength = input.read()
                        val password = ByteArray(passwordLength)
                        readFully(input, password)
                        acceptedUser = String(user)
                        acceptedPassword = String(password)
                        val allowed = (expectedUser == null || expectedUser == acceptedUser) &&
                            (expectedPassword == null || expectedPassword == acceptedPassword)
                        output.write(byteArrayOf(0x01, (if (allowed) 0x00 else 0x01).toByte()))
                        output.flush()
                        if (!allowed) return
                    }
                    if (input.read() != 0x05) return
                    if (input.read() != 0x01) return
                    input.read()
                    if (input.read() != 0x03) return
                    val hostLength = input.read()
                    val host = ByteArray(hostLength)
                    readFully(input, host)
                    val high = input.read()
                    val low = input.read()
                    requestedHost = String(host)
                    requestedPort = (high shl 8) or low
                    output.write(byteArrayOf(0x05, replyCode.toByte(), 0x00, 0x01, 127, 0, 0, 1, 0, 0))
                    output.flush()
                    if (replyCode != 0x00) return
                    val buffer = ByteArray(1024)
                    while (true) {
                        val n = input.read(buffer)
                        if (n < 0) return
                        output.write(buffer, 0, n)
                        output.flush()
                    }
                }
            } catch (_: IOException) {
                // Closed by the test or by the client leaving.
            }
        }

        private fun readFully(input: InputStream, buffer: ByteArray) {
            var read = 0
            while (read < buffer.size) {
                val n = input.read(buffer, read, buffer.size - read)
                if (n < 0) throw IOException("eof")
                read += n
            }
        }

        override fun close() {
            runCatching { server.close() }
        }
    }
}

package com.maestrovpn.tv.utils

import android.os.Build
import java.io.ByteArrayOutputStream
import java.net.InetSocketAddress
import java.net.Socket
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SNIHostName
import javax.net.ssl.SSLSocket

/**
 * One HTTP/1.1 GET dialled to a literal CDN edge address, with the request itself unchanged:
 * Host header, TLS SNI and certificate verification all keep the origin's name, only the TCP
 * destination is pinned.
 *
 * Why not HttpsURLConnection: it takes the destination from the URL, and a URL whose host is an
 * IP makes the CDN edge reject the request (measured: ECONNRESET, because Host carried the IP),
 * while Android offers no per-connection DNS override. So the request is written by hand. This
 * path is only used for control-plane GETs, and only after the canonical origin has failed.
 */
internal object CdnPinnedClient {
    internal data class Response(val status: Int, val body: String)

    private const val HEADER_LIMIT = 8_192
    private const val PORT = 443

    internal fun get(
        host: String,
        address: String,
        path: String,
        bearer: String?,
        timeoutMs: Int,
        limit: Int,
    ): Response? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return null
        var plain: Socket? = null
        var secure: SSLSocket? = null
        return try {
            val socket = Socket()
            plain = socket
            socket.connect(InetSocketAddress(address, PORT), timeoutMs)
            val ssl = HttpsURLConnection.getDefaultSSLSocketFactory()
                .createSocket(socket, host, PORT, true) as SSLSocket
            secure = ssl
            val parameters = ssl.sslParameters
            parameters.serverNames = listOf(SNIHostName(host))
            ssl.sslParameters = parameters
            ssl.soTimeout = timeoutMs
            ssl.startHandshake()
            if (!HttpsURLConnection.getDefaultHostnameVerifier().verify(host, ssl.session)) return null
            val request = StringBuilder(256)
                .append("GET ").append(path).append(" HTTP/1.1\r\n")
                .append("Host: ").append(host).append("\r\n")
                .append("Accept: application/json\r\n")
                .append("Cache-Control: no-store\r\n")
            if (bearer != null) request.append("Authorization: Bearer ").append(bearer).append("\r\n")
            request.append("Connection: close\r\n\r\n")
            val out = ssl.outputStream
            out.write(request.toString().toByteArray(Charsets.UTF_8))
            out.flush()
            parse(readAll(ssl, limit + HEADER_LIMIT), limit)
        } catch (_: Exception) {
            null
        } finally {
            runCatching { secure?.close() }
            if (secure == null) runCatching { plain?.close() }
        }
    }

    private fun readAll(socket: SSLSocket, limit: Int): ByteArray {
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(8_192)
        var total = 0
        while (total < limit) {
            val read = socket.inputStream.read(buffer)
            if (read < 0) break
            if (read == 0) continue
            out.write(buffer, 0, read)
            total += read
        }
        return out.toByteArray()
    }

    /** Pure, so the response shape is unit-tested without a socket. */
    internal fun parse(raw: ByteArray, limit: Int): Response? {
        val separator = headerEnd(raw) ?: return null
        val lines = String(raw, 0, separator, Charsets.ISO_8859_1).split("\r\n")
        val status = lines.firstOrNull()?.split(' ')?.getOrNull(1)?.toIntOrNull() ?: return null
        val headers = LinkedHashMap<String, String>()
        for (line in lines.drop(1)) {
            val colon = line.indexOf(':')
            if (colon > 0) headers[line.substring(0, colon).trim().lowercase()] = line.substring(colon + 1).trim()
        }
        val declared = headers["content-length"]?.toIntOrNull()
        if (declared != null && declared > limit) return null
        val rawBody = raw.copyOfRange(separator + 4, raw.size)
        val body = when {
            headers["transfer-encoding"]?.contains("chunked", ignoreCase = true) == true ->
                dechunk(rawBody) ?: return null
            declared != null -> rawBody.copyOf(minOf(rawBody.size, declared))
            else -> rawBody
        }
        if (body.size > limit) return null
        return Response(status, body.toString(Charsets.UTF_8))
    }

    private fun headerEnd(raw: ByteArray): Int? {
        for (index in 0 until raw.size - 3) {
            if (raw[index] == '\r'.code.toByte() && raw[index + 1] == '\n'.code.toByte() &&
                raw[index + 2] == '\r'.code.toByte() && raw[index + 3] == '\n'.code.toByte()
            ) return index
        }
        return null
    }

    private fun dechunk(raw: ByteArray): ByteArray? {
        val out = ByteArrayOutputStream()
        var cursor = 0
        while (cursor < raw.size) {
            var lineEnd = cursor
            while (lineEnd < raw.size - 1 && !(raw[lineEnd] == '\r'.code.toByte() && raw[lineEnd + 1] == '\n'.code.toByte())) lineEnd++
            if (lineEnd >= raw.size - 1) return null
            val size = String(raw, cursor, lineEnd - cursor, Charsets.ISO_8859_1).substringBefore(';').trim().toIntOrNull(16)
                ?: return null
            cursor = lineEnd + 2
            if (size == 0) return out.toByteArray()
            if (cursor + size > raw.size) return null
            out.write(raw, cursor, size)
            cursor += size + 2
        }
        return out.toByteArray()
    }
}

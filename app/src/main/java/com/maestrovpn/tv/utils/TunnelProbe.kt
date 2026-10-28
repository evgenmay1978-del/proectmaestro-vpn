package com.maestrovpn.tv.utils

import com.maestrovpn.tv.BuildConfig
import java.io.IOException
import java.io.InputStream
import java.net.URL
import java.util.Locale
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

/** What the probe asks for. The panel health endpoint is the dependency the app itself needs. */
internal data class TunnelProbeTarget(val host: String, val port: Int, val path: String) {
    init {
        require(host.isNotBlank() && port in 1..65535 && path.startsWith("/")) { "invalid probe target" }
    }
}

/**
 * One end-to-end probe of the tunnel: SOCKS5 handshake inside the tunnel, then TLS and a real
 * HTTP request to the panel. ok=true means bytes travelled in both directions through the whole
 * chain (app -> SOCKS5 entry -> node/edge -> panel), not merely that the engine accepted a config.
 */
internal data class TunnelProbeResult(
    val ok: Boolean,
    val status: Int?,
    val elapsedMs: Long,
    /** Short stage token for the log, never a host or a credential. */
    val failure: String?,
)

internal object TunnelProbe {
    /**
     * Matches WhiteListWatchdogPolicy.probeTimeoutMillis so a probe can never outlive the watchdog
     * cycle that started it.
     */
    const val TIMEOUT_MS = 9_000
    private const val MAX_STATUS_LINE = 256
    private const val DEADLINE_MESSAGE = "tunnel probe deadline"

    private class Deadline : IOException(DEADLINE_MESSAGE)

    /**
     * The panel health endpoint through the tunnel. Built from BACKEND_URL, so the probe checks the
     * same host the subscription and update paths depend on. Returns null if BACKEND_URL is unusable
     * — the caller then skips probing instead of probing a guessed host.
     */
    fun panelTarget(): TunnelProbeTarget? {
        val url = runCatching { URL(BuildConfig.BACKEND_URL.trimEnd('/') + "/healthz") }.getOrNull() ?: return null
        if (url.protocol.lowercase(Locale.ROOT) != "https") return null
        val host = url.host
        if (host.isNullOrBlank()) return null
        return runCatching { TunnelProbeTarget(host, if (url.port > 0) url.port else 443, url.path) }.getOrNull()
    }

    fun probe(
        socksPort: Int,
        credentials: Socks5Tunnel.Credentials? = null,
        target: TunnelProbeTarget,
        timeoutMs: Int = TIMEOUT_MS,
    ): TunnelProbeResult {
        val started = System.nanoTime()
        val deadline = started + TimeUnit.MILLISECONDS.toNanos(timeoutMs.toLong())
        var socksFailure: Socks5Tunnel.Failure? = null
        val tunnel = Socks5Tunnel.open(socksPort, credentials, target.host, target.port, timeoutMs) { socksFailure = it }
            ?: return TunnelProbeResult(false, null, elapsed(started), token(socksFailure))
        try {
            val status = runCatching { exchange(tunnel, target, deadline) }
            val line = status.getOrNull()
            val error = status.exceptionOrNull()
            val failure = when {
                line != null -> null
                error is Deadline -> "timeout"
                error is IOException -> "tls"
                else -> "http"
            }
            return TunnelProbeResult(line != null, line, elapsed(started), failure)
        } finally {
            runCatching { tunnel.close() }
        }
    }

    private fun exchange(tunnel: java.net.Socket, target: TunnelProbeTarget, deadline: Long): Int? {
        val factory = SSLSocketFactory.getDefault() as SSLSocketFactory
        val tls = factory.createSocket(tunnel, target.host, target.port, false) as SSLSocket
        try {
            tls.soTimeout = remaining(deadline)
            val parameters = tls.sslParameters
            // A probe that a MITM can answer with any valid certificate is worthless as evidence.
            parameters.endpointIdentificationAlgorithm = "HTTPS"
            tls.sslParameters = parameters
            tls.startHandshake()
            val request = "GET " + target.path + " HTTP/1.1\r\nHost: " + target.host +
                "\r\nUser-Agent: maestro-probe\r\nAccept: */*\r\nConnection: close\r\n\r\n"
            val output = tls.outputStream
            output.write(request.toByteArray(Charsets.US_ASCII))
            output.flush()
            tls.soTimeout = remaining(deadline)
            return readStatus(tls.inputStream)
        } finally {
            runCatching { tls.close() }
        }
    }

    private fun readStatus(input: InputStream): Int? {
        val line = StringBuilder(64)
        while (line.length < MAX_STATUS_LINE) {
            val byte = input.read()
            if (byte < 0) return null
            if (byte == '\n'.code) break
            if (byte != '\r'.code) line.append(byte.toChar())
        }
        val code = line.toString().split(' ').getOrNull(1)?.toIntOrNull() ?: return null
        return if (code in 100..599) code else null
    }

    private fun remaining(deadline: Long): Int {
        val left = TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime())
        if (left <= 0) throw Deadline()
        return if (left > Int.MAX_VALUE) Int.MAX_VALUE else left.toInt()
    }

    private fun elapsed(started: Long): Long = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)

    private fun token(failure: Socks5Tunnel.Failure?): String =
        failure?.name?.lowercase(Locale.ROOT) ?: "connect"
}

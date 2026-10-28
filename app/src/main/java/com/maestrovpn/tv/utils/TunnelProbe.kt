package com.maestrovpn.tv.utils

import android.os.Build
import java.io.IOException
import java.io.InputStream
import java.util.Locale
import java.util.concurrent.TimeUnit
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

/** What the probe asks for. Only hosts the applied config routes THROUGH the tunnel may be used. */
internal data class TunnelProbeTarget(val host: String, val port: Int, val path: String) {
    init {
        require(host.isNotBlank() && port in 1..65535 && path.startsWith("/")) { "invalid probe target" }
    }
}

/**
 * One end-to-end probe of the tunnel: SOCKS5 handshake inside the tunnel, then TLS and a real
 * HTTP request. ok=true means bytes travelled in both directions through the whole chain
 * (app -> SOCKS5 entry -> node/edge -> destination), not merely that the engine accepted a config.
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
     *
     * 20 s total and 10 s per target, NOT 9 s/4.5 s: measured on the owner's LTE (28.10.2026) the
     * SAME healthy nodes completed TLS handshakes in 0.2-0.6 s most of the time and stalled for
     * 4.5-9+ s on a sizeable minority of attempts (CZ AnyTLS: 2 of 4 probes to one host sat until
     * the 4.5 s budget expired). A 4.5 s per-target budget therefore reported a WORKING tunnel as
     * dead, and 1.0.178 stopped the tunnel on that verdict. A mobile path needs slack, not speed.
     */
    const val TIMEOUT_MS = 20_000
    private const val MAX_STATUS_LINE = 256
    internal const val MIN_TARGET_MS = 10_000
    private const val DEADLINE_MESSAGE = "tunnel probe deadline"

    private class Deadline : IOException(DEADLINE_MESSAGE)

    /**
     * ⛔ The panel health endpoint CANNOT be a liveness target, even though the audit suggested it.
     * The panel config routes wapmixx.ru through "direct" (rule 2) so the subscription and update
     * paths keep working with the tunnel down; a SOCKS CONNECT to that host therefore leaves the
     * device without ever touching the node, and a perfectly dead tunnel answers 200 — the exact
     * false "connected" this fix exists to remove. Verified against the live /sub config
     * (route.rules: {"domain": ["wapmixx.ru"], "outbound": "direct"} precedes the google suffix rule)
     * and against WhiteListConfig.cdnRoute (only rules pointing at "select" are retargeted; "direct"
     * rules stay untouched, so the CDN path has the same blind spot).
     */

    /**
     * Targets the fleet config routes through the tunnel: sing-box matches the domain of our SOCKS
     * CONNECT against the google/gstatic suffix rule (outbound "select", retargeted to the CDN tag
     * for a CDN session) BEFORE any direct rule. Two independent hosts, so a captive portal or one
     * dead destination is not reported as "the tunnel carries no traffic".
     */
    fun neutralTarget(): TunnelProbeTarget = TunnelProbeTarget("www.google.com", 443, "/generate_204")

    fun gstaticTarget(): TunnelProbeTarget = TunnelProbeTarget("connectivitycheck.gstatic.com", 443, "/generate_204")

    fun targets(): List<TunnelProbeTarget> = listOf(neutralTarget(), gstaticTarget()).distinct()

    /**
     * Per-target budget: the shared timeout split between the targets, but never below
     * MIN_TARGET_MS, so a slow-but-alive handshake is not cut short by the split itself.
     * Pure, so the arithmetic is covered by a unit test instead of a live network.
     */
    fun perTargetBudget(timeoutMs: Int, count: Int): Int = maxOf(MIN_TARGET_MS, timeoutMs / count.coerceAtLeast(1))

    /**
     * Tries every target inside ONE shared budget so a watchdog cycle can never be stretched past
     * probeTimeoutMillis. Any valid HTTP status counts as proof: the request left the device through
     * the tunnel and came back.
     */
    fun probeAny(
        socksPort: Int,
        credentials: Socks5Tunnel.Credentials? = null,
        candidates: List<TunnelProbeTarget> = targets(),
        timeoutMs: Int = TIMEOUT_MS,
    ): TunnelProbeResult {
        if (candidates.isEmpty()) return TunnelProbeResult(false, null, 0, "no-target")
        val started = System.nanoTime()
        val perTarget = perTargetBudget(timeoutMs, candidates.size)
        var last: TunnelProbeResult? = null
        for (target in candidates) {
            val result = probe(socksPort, credentials, target, perTarget)
            if (result.ok) return result
            last = result
        }
        val failed = last ?: return TunnelProbeResult(false, null, elapsed(started), "no-target")
        return TunnelProbeResult(false, failed.status, elapsed(started), failed.failure)
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
            // A probe that a MITM can answer with any valid certificate is worthless as evidence.
            // Endpoint identification inside the handshake needs API 24; on 23 the handshake is
            // verified against the same host right after it, so minSdk 23 keeps a checked probe.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                val parameters = tls.sslParameters
                parameters.endpointIdentificationAlgorithm = "HTTPS"
                tls.sslParameters = parameters
            }
            tls.startHandshake()
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) {
                if (!HttpsURLConnection.getDefaultHostnameVerifier().verify(target.host, tls.session)) {
                    throw IOException("probe hostname mismatch")
                }
            }
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

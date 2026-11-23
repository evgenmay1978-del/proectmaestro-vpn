package com.maestrovpn.tv.utils

import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.ktx.unwrap
import com.maestrovpn.tv.whitelist.TunnelFetch
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import java.io.Closeable
import java.io.IOException
import java.net.URL
import java.util.Locale
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import javax.net.ssl.HttpsURLConnection

/**
 * /sub answered with a non-200 status. The status IS the diagnosis: 402 expired,
 * 404/410 the token no longer exists, 5xx the panel is sick. Collapsing it into
 * "null = timeout" hid a deleted subscription behind a fake network error (21.10.2026).
 */
class SubscriptionHttpException(val status: Int) : IOException("subscription HTTP $status")

/**
 * Keep the /sub HTTP status, for BOTH transports (the pinned CDN edge and the plain
 * HttpsURLConnection). 402 (expired) and 404/410 (token gone) are the diagnosis, not a
 * transient network failure: collapsing them into a plain IOException hid the panel verdict
 * (SubscriptionHealth.report never saw it) and left the in-app «подписка истекла» dialog dead.
 * One decision point on purpose — the two branches had already drifted apart once (D4,
 * регрессия неполного merge-фикса 96a96bd8).
 */
internal fun requireSubscriptionHttpStatus(status: Int) {
    if (status != 200) throw SubscriptionHttpException(status)
}

/** Outcome of a control-plane subscription fetch with the HTTP status preserved. */
sealed interface SubscriptionFetch {
    data class Ok(val body: String) : SubscriptionFetch
    data class HttpError(val status: Int) : SubscriptionFetch
    data object Network : SubscriptionFetch
}

class HTTPClient : Closeable {
    companion object {
        val userAgent by lazy {
            var userAgent = "SFA/"
            userAgent += BuildConfig.VERSION_NAME
            userAgent += " ("
            userAgent += BuildConfig.VERSION_CODE
            userAgent += "; sing-box "
            userAgent += Libbox.version()
            userAgent += "; language "
            userAgent += Locale.getDefault().toLanguageTag().replace("-", "_")
            userAgent += ")"
            userAgent
        }

        /** Control-plane documents are a few KB; anything bigger is not ours. */
        const val SUBSCRIPTION_LIMIT = 512 * 1024
    }

    private val client = Libbox.newHTTPClient()
    private val closed = AtomicBoolean(false)
    private val subscriptionConnection = AtomicReference<HttpsURLConnection?>()

    init {
        client.modernTLS()
    }

    fun getString(url: String, timeoutMs: Long = 15_000): String {
        val cdn = MaestroSub.cdnEndpoints(url)
        if (cdn.isNotEmpty()) {
            // Tunnel-fetch first when the tunnel is up: the direct path to the panel is
            // blocked from Russian networks, and the tunnel's exit nodes reach it. A dead
            // tunnel costs one instant refused connection, then the chain falls through.
            val tunnel = if (TunnelFetch.isAvailable) listOf(MaestroSub.CdnEndpoint(url, viaTunnelProxy = true)) else emptyList()
            return getSubscriptionString(tunnel + listOf(MaestroSub.CdnEndpoint(url)) + cdn, timeoutMs)
        }
        val request = client.newRequest()
        request.setUserAgent(userAgent)
        request.setURL(url)
        val response = request.execute()
        return response.content.unwrap
    }

    private fun getSubscriptionString(endpoints: List<MaestroSub.CdnEndpoint>, timeoutMs: Long): String {
        var failure: IOException? = null
        for (endpoint in endpoints) {
            if (closed.get()) throw IOException("subscription request closed")
            var request: HttpsURLConnection? = null
            var status = 0
            try {
                if (endpoint.pinnedAddress != null) {
                    val parsed = URL(endpoint.url)
                    val path = parsed.path + (parsed.query?.let { "?$it" } ?: "")
                    val response = CdnPinnedClient.get(parsed.host, endpoint.pinnedAddress, path, null,
                        timeoutMs.coerceIn(1, Int.MAX_VALUE.toLong()).toInt(), SUBSCRIPTION_LIMIT)
                        ?: throw IOException("subscription edge unreachable")
                    status = response.status
                    requireSubscriptionHttpStatus(status)
                    return response.body
                }
                if (endpoint.viaTunnelProxy) {
                    // Through the live tunnel: the mixed inbound dials wapmixx.ru out of the
                    // exit node, where the direct path is not blocked. Short timeouts — a
                    // dead tunnel must fail fast, not stall the refresh. A 4xx here is a real
                    // diagnosis and throws immediately, exactly as on the direct path.
                    val connection = TunnelFetch.openConnection(endpoint.url)
                    request = connection
                    subscriptionConnection.set(connection)
                    if (closed.get()) throw IOException("subscription request closed")
                    connection.requestMethod = "GET"
                    connection.instanceFollowRedirects = false
                    connection.useCaches = false
                    connection.connectTimeout = TunnelFetch.PROXY_TIMEOUT_MS
                    connection.readTimeout = TunnelFetch.PROXY_TIMEOUT_MS
                    connection.setRequestProperty("User-Agent", userAgent)
                    connection.setRequestProperty("Accept", "application/json")
                    connection.setRequestProperty("Cache-Control", "no-store")
                    status = connection.responseCode
                    requireSubscriptionHttpStatus(status)
                    return connection.inputStream.bufferedReader().use { it.readText() }
                }
                val connection = URL(endpoint.url).openConnection() as HttpsURLConnection
                request = connection
                subscriptionConnection.set(connection)
                if (closed.get()) throw IOException("subscription request closed")
                connection.requestMethod = "GET"
                connection.instanceFollowRedirects = false
                connection.useCaches = false
                connection.connectTimeout = timeoutMs.coerceIn(1, Int.MAX_VALUE.toLong()).toInt()
                connection.readTimeout = connection.connectTimeout
                connection.setRequestProperty("User-Agent", userAgent)
                connection.setRequestProperty("Accept", "application/json")
                connection.setRequestProperty("Cache-Control", "no-store")
                status = connection.responseCode
                requireSubscriptionHttpStatus(status)
                return connection.inputStream.bufferedReader().use { it.readText() }
            } catch (error: IOException) {
                failure = error
                if (endpoint === endpoints.last() || closed.get() ||
                    (status != 0 && status != -1 && status != 200 && status !in 500..599)
                ) throw error
            } finally {
                subscriptionConnection.compareAndSet(request, null)
                request?.disconnect()
            }
        }
        throw failure ?: IOException("subscription unavailable")
    }

    override fun close() {
        closed.set(true)
        subscriptionConnection.getAndSet(null)?.disconnect()
        client.close()
    }
}

/**
 * Fetch a subscription (or any control-plane URL) and keep the HTTP status.
 *
 * Same hard caller-side timeout as before: libbox's HTTPClient sets no client timeout, so a
 * panel that completes TLS and then stalls would hang the caller forever. The native call is
 * blocking and ignores cancellation — run it detached and bound only the WAIT; the client is
 * closed on timeout, which aborts the in-flight request.
 */
@OptIn(DelicateCoroutinesApi::class)
suspend fun httpFetchSubscription(url: String, timeoutMs: Long = 15_000): SubscriptionFetch {
    val result = CompletableDeferred<SubscriptionFetch>()
    val client = HTTPClient()
    GlobalScope.launch(Dispatchers.IO) {
        val value = try {
            SubscriptionFetch.Ok(client.getString(url, timeoutMs))
        } catch (e: SubscriptionHttpException) {
            SubscriptionFetch.HttpError(e.status)
        } catch (t: Throwable) {
            SubscriptionFetch.Network
        }
        result.complete(value)
    }
    return try {
        val waitMs = subscriptionWaitMs(url, timeoutMs)
        withTimeoutOrNull(waitMs) { result.await() } ?: SubscriptionFetch.Network
    } finally {
        runCatching { client.close() }
    }
}

/**
 * How long httpFetchSubscription waits for the detached fetch. The old formula
 * (timeoutMs * 2 with a CDN fallback) only ever covered ~2 attempts of a 7-endpoint chain —
 * and now the chain starts with a proxy attempt that can itself take up to 2 x
 * PROXY_TIMEOUT_MS (3 s connect + 3 s read). The budget is sized for the worst case the
 * chain can still need: proxy (6 s) + the direct and CDN attempts the old budget already
 * covered. The proxy must not eat that budget. Without a proxy attempt the old formula is
 * unchanged — including foreign URLs, where the proxy is never attempted.
 */
internal fun subscriptionWaitMs(url: String, timeoutMs: Long): Long {
    val hasFallback = MaestroSub.cdnFallbackUrl(url) != null
    val waitMs = if (hasFallback) timeoutMs * 2 else timeoutMs
    return if (hasFallback && TunnelFetch.isAvailable) waitMs + 2L * TunnelFetch.PROXY_TIMEOUT_MS else waitMs
}

/**
 * Legacy nullable wrapper for callers that only need the body. Prefer
 * [httpFetchSubscription] where the status matters (subscription health, activation).
 */
suspend fun httpGetStringTimed(url: String, timeoutMs: Long = 15_000): String? =
    (httpFetchSubscription(url, timeoutMs) as? SubscriptionFetch.Ok)?.body

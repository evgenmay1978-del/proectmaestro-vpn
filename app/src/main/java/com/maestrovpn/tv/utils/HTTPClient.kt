package com.maestrovpn.tv.utils

import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.ktx.unwrap
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import java.io.Closeable
import java.util.Locale

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
    }

    private val client = Libbox.newHTTPClient()

    init {
        client.modernTLS()
    }

    fun getString(url: String): String {
        val request = client.newRequest()
        request.setUserAgent(userAgent)
        request.setURL(url)
        val response = request.execute()
        return response.content.unwrap
    }

    override fun close() {
        client.close()
    }
}

/**
 * Fetch [url] with a HARD caller-side timeout, returning null on timeout or any error.
 *
 * libbox's HTTPClient sets no http.Client.Timeout, so a panel that completes the TLS
 * handshake and then stalls would hang the caller FOREVER — this is the root cause of the
 * "spinner never finishes / app freezes" reports (the update worker, claim, edit/new-profile
 * and the home account card all fetch /sub this way). The native execute() is a blocking call
 * that coroutine cancellation can't interrupt, so we run it on a DETACHED job and bound only
 * the WAIT: on timeout the caller gets null immediately (treat as "panel unreachable") while
 * the abandoned native socket is left for the OS to time out. Callers MUST treat null as a
 * normal failure (retry / show an error / fall back to cached) — never as success.
 */
@OptIn(DelicateCoroutinesApi::class)
suspend fun httpGetStringTimed(url: String, timeoutMs: Long = 15_000): String? {
    val result = CompletableDeferred<String?>()
    // Hold the client OUTSIDE the detached job so the caller can close() it on timeout,
    // aborting the in-flight native request and releasing its socket FD instead of leaking
    // it until the OS times out (the leak piled up on slow TVs that hit this path often).
    val client = HTTPClient()
    GlobalScope.launch(Dispatchers.IO) {
        try {
            result.complete(client.getString(url))
        } catch (t: Throwable) {
            result.complete(null)
        }
    }
    return try {
        withTimeoutOrNull(timeoutMs) { result.await() }
    } finally {
        runCatching { client.close() }
    }
}

package com.maestrovpn.tv.aivpn

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URLEncoder

/**
 * Pulls the personal NeuroProtocol (AIVPN) connection key out of the subscription panel.
 *
 * The panel hands out one key per (subscription, device) pair:
 * GET /aivpn/<token>?device=<id>, on the same scheme/host/port as the /sub/<token> subscription
 * URL the app already has. The panel accepts the request only with the exact client User-Agent,
 * so the caller passes HTTPClient.userAgent and this file builds no User-Agent of its own.
 *
 * The key and the token are secrets. This file never logs, never puts them into an exception
 * message and never echoes the server body back to the UI.
 *
 * Everything here is plain JVM (java.net + kotlinx.serialization), so it can be unit-tested
 * without the Android framework.
 */
object AivpnProvisioner {

    private const val CONNECT_TIMEOUT_MS = 10_000
    private const val READ_TIMEOUT_MS = 15_000

    /** A refusal body is one short sentence; anything bigger is an error page, not ours. */
    private const val MAX_BODY_BYTES = 4 * 1024

    /** Status of a request that never reached the panel. */
    private const val NO_RESPONSE = -1

    private const val STATUS_OK = 200
    private const val STATUS_PAYMENT_REQUIRED = 402
    private const val STATUS_FORBIDDEN = 403
    private const val STATUS_NOT_FOUND = 404
    private const val STATUS_GONE = 410

    /** Same shape the panel uses for subscription tokens (see whitelist/WhiteListBalance.kt). */
    private val subPath = Regex("^/sub/([A-Za-z0-9._~+\\-]+=*)\$")

    private val json = Json { ignoreUnknownKeys = true }

    /** Outcome of a provisioning attempt. */
    sealed interface Result {
        /** The panel returned a usable aivpn:// key, personal for this device. */
        data class Ok(val key: String) : Result

        /** The panel answered, but this subscription or this device is not allowed. */
        data class Denied(val reason: Reason) : Result

        /** No usable answer (no network, panel error, malformed reply). A retry may help. */
        data object Unavailable : Result

        /** The app has no trusted /sub/ URL, so there is no token to ask with. */
        data object NotSubscribed : Result
    }

    /**
     * Why the panel said no. Deliberately coarse: the raw answer never reaches the UI, so it can
     * never echo a token that the panel may have put into its error text.
     */
    enum class Reason { SubscriptionInactive, DeviceLimit, Gated, Forbidden }

    /** Raw answer of the transport layer. [body] is truncated; null when the server sent none. */
    data class Response(val status: Int, val body: String?)

    // ------------------------------------------------------------------- url

    /** True when [url] is a plain https /sub/<token> URL whose token can be derived. */
    fun isSubscriptionUrl(url: String?): Boolean = parseSubscriptionUrl(url) != null

    /**
     * Turns the subscription URL https://host:port/sub/<token>?whatever into
     * https://host:port/aivpn/<token>?device=<deviceId>.
     *
     * Returns null for anything that is not a plain https /sub/<token> URL (http, userinfo,
     * another path, extra path segments) and for a blank device id: asking with no device would
     * fetch a login-wide key instead of this device's key.
     */
    fun buildUrl(subscriptionUrl: String?, deviceId: String?): String? {
        val subscription = parseSubscriptionUrl(subscriptionUrl) ?: return null
        val device = deviceId?.trim().orEmpty()
        if (device.isEmpty()) return null
        val port = subscription.port
        val origin = if (port > 0 && port != 443) {
            "https://" + subscription.host + ":" + port
        } else {
            "https://" + subscription.host
        }
        return origin + "/aivpn/" + subscription.token +
            "?device=" + URLEncoder.encode(device, Charsets.UTF_8.name())
    }

    private class Subscription(val host: String, val port: Int, val token: String)

    private fun parseSubscriptionUrl(url: String?): Subscription? {
        val uri = runCatching { URI(url) }.getOrNull() ?: return null
        if (!uri.scheme.equals("https", ignoreCase = true)) return null
        if (uri.rawUserInfo != null) return null
        val host = uri.host ?: return null
        if (host.isEmpty()) return null
        val token = tokenOf(uri) ?: return null
        return Subscription(host, uri.port, token)
    }

    private fun tokenOf(uri: URI): String? {
        val path = uri.path ?: return null
        // The panel token may end in base64 "=" padding; the regex keeps it out of group 1, so the
        // token is the whole path tail after the regex accepted it.
        if (!subPath.matches(path)) return null
        return path.removePrefix("/sub/").takeIf { it.isNotEmpty() }
    }

    // ----------------------------------------------------------------- fetch

    /**
     * Fetches this device's key. Blocking network call: call it from Dispatchers.IO.
     *
     * Nothing is logged: an exception message could carry the token, and a key must never reach
     * logcat.
     */
    fun fetch(
        subscriptionUrl: String?,
        userAgent: String,
        deviceId: String?,
    ): Result = fetch(subscriptionUrl, userAgent, deviceId, ::get)

    /** Same request with an injectable transport: [openConnection] is a test seam, production uses [get]. */
    fun fetch(
        subscriptionUrl: String?,
        userAgent: String,
        deviceId: String?,
        openConnection: (String, String) -> Response,
    ): Result {
        val url = buildUrl(subscriptionUrl, deviceId) ?: return Result.NotSubscribed
        return interpret(openConnection(url, userAgent))
    }

    internal fun interpret(response: Response): Result = interpret(response.status, response.body)

    /** Maps the panel answer to [Result]. Errors are plain text on this panel, success is JSON. */
    internal fun interpret(status: Int, body: String?): Result {
        val text = body.orEmpty()
        return when (status) {
            STATUS_OK -> parseKey(text)?.let { Result.Ok(it) } ?: Result.Unavailable
            STATUS_PAYMENT_REQUIRED, STATUS_FORBIDDEN -> Result.Denied(classify(text))
            STATUS_NOT_FOUND, STATUS_GONE -> Result.NotSubscribed
            else -> Result.Unavailable
        }
    }

    // --------------------------------------------------------------- what to keep

    /**
     * The key the app keeps after [result]. A fresh key from the panel always wins, so a key pasted
     * by hand before the subscription handed one out is replaced; a failed attempt keeps [saved].
     */
    fun keyToStore(saved: String, result: Result): String = when (result) {
        is Result.Ok -> result.key
        else -> saved
    }

    /**
     * A refusal is worth a message only while the app has nothing to fall back to: with an older key
     * on hand the screen stays quiet instead of explaining a failure the user does not feel.
     */
    fun shouldReportFailure(saved: String, current: String): Boolean =
        saved.isBlank() && current.isBlank()

    private fun classify(body: String): Reason {
        val text = (parseServerMessage(body) ?: body).lowercase()
        return when {
            text.contains("device limit") -> Reason.DeviceLimit
            text.contains("aivpn unavailable") -> Reason.Gated
            text.contains("expired") || text.contains("inactive") -> Reason.SubscriptionInactive
            else -> Reason.Forbidden
        }
    }

    /** Success body is {"key": "aivpn://..."}. */
    private fun parseKey(body: String): String? {
        val trimmed = body.trim()
        if (!trimmed.startsWith("{")) return null
        return runCatching {
            val key = (json.parseToJsonElement(trimmed) as? JsonObject)
                ?.get("key")?.jsonPrimitive?.contentOrNull?.trim()
            key?.takeIf { it.startsWith(AivpnKey.SCHEME) }
        }.getOrNull()
    }

    /** Refusals look like {"error": "..."} in the contract, but the panel sends plain text too. */
    private fun parseServerMessage(body: String): String? {
        val trimmed = body.trim()
        if (!trimmed.startsWith("{")) return null
        return runCatching {
            (json.parseToJsonElement(trimmed) as? JsonObject)
                ?.get("error")?.jsonPrimitive?.contentOrNull
        }.getOrNull()
    }

    private fun get(url: String, userAgent: String): Response {
        var connection: HttpURLConnection? = null
        return try {
            val opened = URI(url).toURL().openConnection() as HttpURLConnection
            connection = opened
            opened.instanceFollowRedirects = false
            opened.useCaches = false
            opened.requestMethod = "GET"
            opened.connectTimeout = CONNECT_TIMEOUT_MS
            opened.readTimeout = READ_TIMEOUT_MS
            opened.setRequestProperty("User-Agent", userAgent)
            opened.setRequestProperty("Accept", "application/json")
            opened.setRequestProperty("Cache-Control", "no-store")
            val status = opened.responseCode
            val stream = if (status in 200..299) opened.inputStream else opened.errorStream
            Response(status, stream?.use { readCapped(it) })
        } catch (_: Exception) {
            // Swallowed on purpose: the message of an IOException here can contain the URL, and
            // the URL contains the token.
            Response(NO_RESPONSE, null)
        } finally {
            runCatching { connection?.disconnect() }
        }
    }

    private fun readCapped(stream: InputStream): String {
        val buffer = ByteArrayOutputStream()
        val chunk = ByteArray(1024)
        while (buffer.size() < MAX_BODY_BYTES) {
            val read = stream.read(chunk)
            if (read <= 0) break
            buffer.write(chunk, 0, minOf(read, MAX_BODY_BYTES - buffer.size()))
        }
        return String(buffer.toByteArray(), Charsets.UTF_8)
    }
}

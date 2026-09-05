package com.maestrovpn.tv.whitelist

import android.net.Network
import android.os.SystemClock
import com.maestrovpn.tv.bg.UpdateProfileWork
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.net.URI
import java.net.URL
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.net.ssl.HttpsURLConnection

internal data class WhiteListRuntimeRoute(
    val routeId: String, val label: String,
    val transportProfileId: String, val transportReleaseId: String, val compatibilityPresetId: String,
    val address: String, val port: Int, val serverName: String, val host: String,
    val path: String, val clientId: String, val encryption: String,
) {
    val tag: String get() = "cdn:$routeId"
    override fun toString(): String = "WhiteListRuntimeRoute(redacted)"
}

internal data class WhiteListRuntime(
    val projectionVersion: Long, val desiredGeneration: Long,
    val deadlineMillis: Long, val profiles: List<WhiteListRuntimeRoute>,
) {
    fun fresh(nowMillis: Long): Boolean = nowMillis < deadlineMillis
    override fun toString(): String = "WhiteListRuntime(redacted)"
}

internal object WhiteListRuntimeClient {
    private const val LIMIT = 65_536
    private const val REQUEST_LIMIT_MS = 1_500L
    private val deadlineExecutor = Executors.newSingleThreadScheduledExecutor { runnable ->
        Thread(runnable, "cdn-request-deadline").apply { isDaemon = true }
    }
    private val subscriptionPath = Regex("^/sub/([A-Za-z0-9._~+\\-]+=*)$")
    private val digest = Regex("^[0-9a-f]{64}$")
    private val id = Regex("^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
    private val uuid = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
    private val json = Json { ignoreUnknownKeys = false }

    suspend fun fetch(subscriptionUrl: String, network: Network): WhiteListRuntime? = withContext(Dispatchers.IO) {
        if (!UpdateProfileWork.isTrustedSubUrl(subscriptionUrl)) return@withContext null
        fetch(subscriptionUrl, SystemClock::elapsedRealtime) { network.openConnection(it) as HttpsURLConnection }
    }

    internal fun fetch(
        subscriptionUrl: String,
        clock: () -> Long,
        open: (URL) -> HttpsURLConnection,
    ): WhiteListRuntime? {
        var connection: HttpsURLConnection? = null
        var deadline: java.util.concurrent.ScheduledFuture<*>? = null
        val started = clock()
        return try {
            val source = URI(subscriptionUrl)
            if (source.scheme != "https" || source.rawUserInfo != null || source.host.isNullOrBlank() ||
                source.rawFragment != null || (source.port != -1 && source.port !in 1..65_535)
            ) return null
            val token = subscriptionPath.matchEntire(source.path.orEmpty())?.groupValues?.get(1) ?: return null
            if (token == "." || token == "..") return null
            val endpoint = URI("https", null, source.host, source.port, "/account/whitelist-runtime", null, null).toURL()
            val request = open(endpoint)
            connection = request
            request.requestMethod = "GET"
            request.instanceFollowRedirects = false
            request.useCaches = false
            request.connectTimeout = REQUEST_LIMIT_MS.toInt()
            request.readTimeout = REQUEST_LIMIT_MS.toInt()
            request.setRequestProperty("Authorization", "Bearer $token")
            request.setRequestProperty("Accept", "application/json")
            request.setRequestProperty("Cache-Control", "no-store")
            deadline = deadlineExecutor.schedule({ request.disconnect() }, REQUEST_LIMIT_MS, TimeUnit.MILLISECONDS)
            if (request.responseCode != 200 || request.contentLength > LIMIT) return null
            val bytes = request.inputStream.use { it.readBytesBounded(LIMIT) } ?: return null
            if (clock() - started !in 0..REQUEST_LIMIT_MS) return null
            parse(bytes.toString(Charsets.UTF_8), started, clock())
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        } finally {
            deadline?.cancel(false)
            connection?.disconnect()
        }
    }

    private fun java.io.InputStream.readBytesBounded(limit: Int): ByteArray? {
        val bytes = ByteArray(limit + 1)
        var size = 0
        while (size < bytes.size) {
            val count = read(bytes, size, bytes.size - size)
            if (count < 0) break
            if (count == 0) continue
            size += count
        }
        return if (size > limit) null else bytes.copyOf(size)
    }

    internal fun parse(raw: String, requestStartedMillis: Long, nowMillis: Long): WhiteListRuntime? = try {
        require(raw.toByteArray(Charsets.UTF_8).size <= LIMIT)
        val root = json.parseToJsonElement(raw) as JsonObject
        require(root.keys == setOf("schema_version", "issued_at_unix", "fresh_until_unix", "projection_version", "desired_generation", "profiles"))
        require(root.number("schema_version") == 1L)
        val issued = root.number("issued_at_unix")
        val fresh = root.number("fresh_until_unix")
        require(issued > 0 && fresh > issued && fresh - issued in 1..5)
        require(requestStartedMillis >= 0 && nowMillis >= requestStartedMillis && requestStartedMillis <= Long.MAX_VALUE - 5_000)
        val expires = requestStartedMillis + (fresh - issued) * 1_000
        require(nowMillis < expires)
        val projection = root.number("projection_version")
        val generation = root.number("desired_generation")
        require(projection > 0 && generation > 0)
        val items = root["profiles"] as JsonArray
        require(items.size in 1..16)
        val profiles = items.map { item ->
            val p = item as JsonObject
            require(p.keys == setOf("route_id", "label", "transport_profile_id", "transport_release_id", "compatibility_preset_id", "address", "port", "server_name", "host", "path", "client_id", "encryption"))
            val route = WhiteListRuntimeRoute(
                p.string("route_id"), p.string("label"), p.string("transport_profile_id"),
                p.string("transport_release_id"), p.string("compatibility_preset_id"),
                p.string("address"), p.number("port").also { require(it == 443L) }.toInt(),
                p.string("server_name"), p.string("host"), p.string("path"),
                p.string("client_id"), p.string("encryption"),
            )
            require(digest.matches(route.routeId) && route.label.toByteArray(Charsets.UTF_8).size in 1..255 && route.label.trim() == route.label &&
                route.label.none { it.isISOControl() || it.category in setOf(CharCategory.FORMAT, CharCategory.NON_SPACING_MARK,
                    CharCategory.COMBINING_SPACING_MARK, CharCategory.ENCLOSING_MARK) })
            require(id.matches(route.transportProfileId) && id.matches(route.transportReleaseId) && id.matches(route.compatibilityPresetId))
            require(validHost(route.address) && validHost(route.serverName) && route.serverName == route.host)
            require(uuid.matches(route.clientId))
            require(route.path.length in 2..2048 && route.path.startsWith("/") && !route.path.startsWith("//") &&
                route.path.all { it.code in 0x21..0x7e } && route.path.none { it in "?#\\" } &&
                !route.path.contains("/../") && !route.path.endsWith("/.."))
            require(route.encryption.startsWith("mlkem768x25519plus.native.0rtt."))
            val material = route.encryption.removePrefix("mlkem768x25519plus.native.0rtt.")
            // 1184 bytes of canonical unpadded base64url: the final sextet has two zero bits.
            require(material.length == 1_579 && material.all { it in "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_" } &&
                "AEIMQUYcgkosw048".contains(material.last()))
            route
        }
        require(profiles.map { it.routeId }.distinct().size == profiles.size)
        require(profiles.map { it.label }.distinct().size == profiles.size)
        require(profiles.map { it.clientId }.distinct().size == profiles.size)
        WhiteListRuntime(projection, generation, expires, profiles)
    } catch (_: Exception) { null }

    private fun validHost(value: String): Boolean = value.length in 1..253 &&
        value == value.lowercase(java.util.Locale.ROOT) && value.split('.').all {
            it.matches(Regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$"))
        }

    private fun JsonObject.number(key: String): Long {
        val value = this[key] as JsonPrimitive
        require(!value.isString)
        return requireNotNull(value.longOrNull)
    }
    private fun JsonObject.string(key: String): String {
        val value = this[key] as JsonPrimitive
        require(value.isString)
        return value.content
    }
}

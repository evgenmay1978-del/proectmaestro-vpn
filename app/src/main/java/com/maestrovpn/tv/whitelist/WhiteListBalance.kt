package com.maestrovpn.tv.whitelist

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.longOrNull
import java.math.BigDecimal
import java.net.URI
import java.net.URL
import java.text.DecimalFormat
import java.text.DecimalFormatSymbols
import java.util.Locale
import javax.net.ssl.HttpsURLConnection

/** Account display data only: publicationVerdict is not permission to connect. */
@Serializable
data class WhiteListBalance(
    @SerialName("included_remaining_bytes") val includedRemainingBytes: Long,
    @SerialName("purchased_remaining_bytes") val purchasedRemainingBytes: Long,
    @SerialName("available_bytes") val availableBytes: Long,
    @SerialName("period_ends_at_unix") val periodEndsAtUnix: Long,
    @SerialName("primary_access_state") val primaryAccessState: String,
    @SerialName("publication_verdict") val publicationVerdict: String,
) {
    init {
        require(includedRemainingBytes >= 0 && purchasedRemainingBytes >= 0 && availableBytes >= 0)
        require(periodEndsAtUnix >= 0)
    }
}

object WhiteListBalanceClient {
    private const val MAX_RESPONSE_BYTES = 16_384
    private const val TIMEOUT_MS = 10_000
    private val subscriptionPath = Regex("^/sub/([A-Za-z0-9._~+\\-]+=*)$")
    private val json = Json { ignoreUnknownKeys = true }

    /** Caller selects a trusted profile; transport still rejects unsafe URL shapes. */
    suspend fun fetch(subscriptionUrl: String): WhiteListBalance? = withContext(Dispatchers.IO) {
        fetch(subscriptionUrl) { it.openConnection() as HttpsURLConnection }
    }

    // Internal factory keeps the actual request/response boundary testable without live credentials.
    internal fun fetch(
        subscriptionUrl: String,
        openConnection: (URL) -> HttpsURLConnection,
    ): WhiteListBalance? {
        var connection: HttpsURLConnection? = null
        return try {
            val source = URI(subscriptionUrl)
            if (!source.scheme.equals("https", ignoreCase = true) || source.rawUserInfo != null ||
                source.host.isNullOrBlank() || (source.port != -1 && source.port !in 1..65_535)
            ) return null
            if (!source.rawPath.orEmpty().startsWith("/sub/")) return null
            val token = subscriptionPath.matchEntire(source.path.orEmpty())?.groupValues?.get(1)
                ?: return null
            if (token == "." || token == "..") return null
            val endpoint = URI("https", null, source.host, source.port, "/account/whitelist-balance", null, null).toURL()
            val request = openConnection(endpoint)
            connection = request
            request.requestMethod = "GET"
            request.instanceFollowRedirects = false
            request.useCaches = false
            request.connectTimeout = TIMEOUT_MS
            request.readTimeout = TIMEOUT_MS
            request.setRequestProperty("Authorization", "Bearer $token")
            request.setRequestProperty("Accept", "application/json")
            request.setRequestProperty("Cache-Control", "no-store")
            if (request.responseCode != HttpsURLConnection.HTTP_OK || request.contentLength > MAX_RESPONSE_BYTES) return null

            val buffer = ByteArray(MAX_RESPONSE_BYTES + 1)
            var used = 0
            request.inputStream.use { input ->
                while (used < buffer.size) {
                    val count = input.read(buffer, used, buffer.size - used)
                    if (count < 0) break
                    used += count
                }
            }
            if (used > MAX_RESPONSE_BYTES) return null
            parse(String(buffer, 0, used, Charsets.UTF_8))
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            // Unavailable, unauthorised or malformed is unknown, never a fabricated zero balance.
            null
        } finally {
            connection?.disconnect()
        }
    }

    private fun parse(raw: String): WhiteListBalance? {
        val fields = json.parseToJsonElement(raw) as? JsonObject ?: return null
        for (name in listOf("included_remaining_bytes", "purchased_remaining_bytes", "available_bytes", "period_ends_at_unix")) {
            val value = fields[name] as? JsonPrimitive ?: return null
            if (value.isString || value.longOrNull == null) return null
        }
        for (name in listOf("primary_access_state", "publication_verdict")) {
            if ((fields[name] as? JsonPrimitive)?.isString != true) return null
        }
        return json.decodeFromJsonElement<WhiteListBalance>(fields)
    }
}

/** null hides disabled/unknown data; pending numbers must not be presented as current. */
fun WhiteListBalance.displayText(): String? {
    if (publicationVerdict == "DISABLED") return null
    if (primaryAccessState != "active" && primaryAccessState != "expired") return null
    return when (publicationVerdict) {
        "PROJECTION_PENDING", "PROJECTION_STALE" -> "CDN: обновляется"
        "PRIMARY_EXPIRED" -> if (primaryAccessState == "expired") {
            "CDN: ${decimalGb(purchasedRemainingBytes)} ГБ заморожено"
        } else null
        "NO_BALANCE" -> if (primaryAccessState == "active" && availableBytes == 0L) "CDN: 0 ГБ" else null
        "PUBLISHABLE" -> if (primaryAccessState == "active") "CDN: осталось ${decimalGb(availableBytes)} ГБ" else null
        else -> null
    }
}

private fun decimalGb(bytes: Long): String {
    if (bytes in 1L..9_999_999L) return "< 0,01"
    val format = DecimalFormat("0.##", DecimalFormatSymbols(Locale("ru", "RU")))
    return format.format(BigDecimal.valueOf(bytes, 9))
}

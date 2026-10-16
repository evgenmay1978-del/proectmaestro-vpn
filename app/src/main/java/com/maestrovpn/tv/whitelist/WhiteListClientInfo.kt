package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.longOrNull

enum class WhiteListState {
    DISABLED,
    PROVISIONING,
    ACTIVE,
    GRACE,
    SUSPENDED,
    ERROR,
    EXPIRED,
    UNKNOWN,
}

enum class WhiteListBillingState {
    OFF,
    SHADOW,
    REAL,
    FREE,
    UNKNOWN,
}

data class WhiteListDisplayModel(
    val state: WhiteListState,
    val transportProfileId: String?,
    val transportReleaseId: String?,
    val preset: String?,
    val billingState: WhiteListBillingState,
    val usageBytes: Long?,
    val remainingLimitBytes: Long?,
    val suspensionReason: String?,
    val edgeIds: List<String>,
    val heartbeatEnabled: Boolean,
) {
    val runtimeEligible: Boolean
        get() = (state == WhiteListState.ACTIVE || state == WhiteListState.GRACE) &&
            heartbeatEnabled &&
            transportProfileId != null &&
            transportReleaseId != null &&
            preset != null &&
            edgeIds.isNotEmpty()
}

object WhiteListClientInfoParser {
    private const val MAX_EDGE_COUNT = 16
    private const val MAX_REASON_LENGTH = 160
    private val opaqueId = Regex("^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")

    fun parseInfoResponse(raw: String, isTelevision: Boolean): WhiteListDisplayModel? {
        if (isTelevision) return null
        return runCatching { parseMobile(raw) }.getOrNull()
    }

    private fun parseMobile(raw: String): WhiteListDisplayModel? {
        val root = Json.parseToJsonElement(raw) as? JsonObject
            ?: throw IllegalArgumentException("root must be an object")
        val whiteListElement = root["white_list"] ?: return null
        val whiteList = whiteListElement as? JsonObject
            ?: throw IllegalArgumentException("white_list must be an object")

        val state = enumValue<WhiteListState>(requiredString(whiteList, "state"))
        val billingState = optionalString(whiteList, "billing_state")
            ?.let { enumValue<WhiteListBillingState>(it) }
            ?: WhiteListBillingState.UNKNOWN
        val transportProfileId = optionalOpaqueId(whiteList, "transport_profile_id")
        val transportReleaseId = optionalOpaqueId(whiteList, "transport_release_id")
        val preset = optionalOpaqueId(whiteList, "preset")
        val usageBytes = optionalNonNegativeLong(whiteList, "usage_bytes")
        val remainingLimitBytes = optionalNonNegativeLong(whiteList, "remaining_limit_bytes")
        val suspensionReason = optionalReason(whiteList, "suspension_reason")
        val edgeIds = optionalEdgeIds(whiteList)
        val heartbeatEnabled = optionalBoolean(whiteList, "heartbeat_enabled") ?: false

        return WhiteListDisplayModel(
            state = state,
            transportProfileId = transportProfileId,
            transportReleaseId = transportReleaseId,
            preset = preset,
            billingState = billingState,
            usageBytes = usageBytes,
            remainingLimitBytes = remainingLimitBytes,
            suspensionReason = suspensionReason,
            edgeIds = edgeIds,
            heartbeatEnabled = heartbeatEnabled,
        )
    }

    private inline fun <reified T : Enum<T>> enumValue(raw: String): T =
        enumValues<T>().firstOrNull { it.name == raw } ?: enumValues<T>().first { it.name == "UNKNOWN" }

    private fun requiredString(source: JsonObject, name: String): String =
        optionalString(source, name) ?: throw IllegalArgumentException("$name is required")

    private fun optionalString(source: JsonObject, name: String): String? {
        val value = source[name] ?: return null
        if (value === JsonNull) return null
        val primitive = value as? JsonPrimitive
            ?: throw IllegalArgumentException("$name must be a string")
        if (!primitive.isString) throw IllegalArgumentException("$name must be a string")
        return primitive.content
    }

    private fun optionalOpaqueId(source: JsonObject, name: String): String? =
        optionalString(source, name)?.also {
            require(opaqueId.matches(it)) { "$name is invalid" }
        }

    private fun optionalNonNegativeLong(source: JsonObject, name: String): Long? {
        val value = source[name] ?: return null
        if (value === JsonNull) return null
        val primitive = value as? JsonPrimitive
            ?: throw IllegalArgumentException("$name must be an integer")
        if (primitive.isString) throw IllegalArgumentException("$name must be an integer")
        val parsed = primitive.longOrNull
            ?: throw IllegalArgumentException("$name must be an integer")
        require(parsed >= 0) { "$name must be non-negative" }
        return parsed
    }

    private fun optionalReason(source: JsonObject, name: String): String? {
        val reason = optionalString(source, name) ?: return null
        if (reason.isBlank()) return null
        require(reason.length <= MAX_REASON_LENGTH) { "$name is too long" }
        require(reason.none(Char::isISOControl)) { "$name contains a control character" }
        return reason
    }

    private fun optionalEdgeIds(source: JsonObject): List<String> {
        val value = source["edge_ids"] ?: return emptyList()
        if (value === JsonNull) return emptyList()
        val values = value as? JsonArray
            ?: throw IllegalArgumentException("edge_ids must be an array")
        require(values.size <= MAX_EDGE_COUNT) { "too many edge_ids" }
        val edgeIds = values.mapIndexed { index, element ->
            val primitive = element as? JsonPrimitive
                ?: throw IllegalArgumentException("edge_ids[$index] must be a string")
            if (!primitive.isString || !opaqueId.matches(primitive.content)) {
                throw IllegalArgumentException("edge_ids[$index] is invalid")
            }
            primitive.content
        }
        require(edgeIds.distinct().size == edgeIds.size) { "edge_ids must be unique" }
        return edgeIds
    }

    private fun optionalBoolean(source: JsonObject, name: String): Boolean? {
        val value = source[name] ?: return null
        if (value === JsonNull) return null
        val primitive = value as? JsonPrimitive
            ?: throw IllegalArgumentException("$name must be a boolean")
        if (primitive.isString) throw IllegalArgumentException("$name must be a boolean")
        return primitive.booleanOrNull
            ?: throw IllegalArgumentException("$name must be a boolean")
    }
}

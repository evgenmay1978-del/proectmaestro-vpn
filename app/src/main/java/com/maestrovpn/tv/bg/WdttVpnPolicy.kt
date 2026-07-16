package com.maestrovpn.tv.bg

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

internal object WdttVpnPolicy {
    data class PackageOverrides(
        val include: Set<String>? = null,
        val exclude: Set<String>? = null,
    )

    fun hasWdttOutbound(content: String): Boolean = runCatching {
        val root = Json.parseToJsonElement(content) as? JsonObject ?: return@runCatching false
        // `as?` casts (never the throwing jsonArray/jsonObject accessors) so a malformed value
        // for one key can't abort the scan of the other. vk-turn is emitted as a top-level
        // "endpoints" entry (sing-box 1.13 removed the legacy `wireguard` OUTBOUND); older/cached
        // profiles may still carry it under "outbounds", so scan BOTH — the anti-loop TUN bypass
        // must trigger for either shape, else the WDTT child's VK/TURN/DNS sockets loop through the tun.
        fun taggedIn(key: String): Boolean =
            (root[key] as? JsonArray)?.any { node ->
                ((node as? JsonObject)?.get("tag") as? JsonPrimitive)?.contentOrNull == WdttManager.OUTBOUND_TAG
            } ?: false
        taggedIn("endpoints") || taggedIn("outbounds")
    }.getOrDefault(false)

    fun resolvePackageOverrides(
        perAppEnabled: Boolean,
        includeMode: Boolean,
        appList: Set<String>,
        appPackage: String,
        wdttOwnPackageBypass: Boolean,
    ): PackageOverrides {
        if (!perAppEnabled) {
            return PackageOverrides(exclude = setOf(appPackage).takeIf { wdttOwnPackageBypass })
        }
        return if (includeMode) {
            PackageOverrides(include = if (wdttOwnPackageBypass) appList - appPackage else appList + appPackage)
        } else {
            PackageOverrides(exclude = if (wdttOwnPackageBypass) appList + appPackage else appList - appPackage)
        }
    }
}

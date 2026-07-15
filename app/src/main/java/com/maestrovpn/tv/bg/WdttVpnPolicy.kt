package com.maestrovpn.tv.bg

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

internal object WdttVpnPolicy {
    data class PackageOverrides(
        val include: Set<String>? = null,
        val exclude: Set<String>? = null,
    )

    fun hasWdttOutbound(content: String): Boolean = runCatching {
        val outbounds = Json.parseToJsonElement(content).jsonObject["outbounds"]?.jsonArray
            ?: return@runCatching false
        outbounds.any { outbound ->
            outbound.jsonObject["tag"]?.jsonPrimitive?.contentOrNull == WdttManager.OUTBOUND_TAG
        }
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

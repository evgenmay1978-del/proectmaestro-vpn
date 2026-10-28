package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Makes the applied config capture IPv6, the way upstream sing-box does by default.
 *
 * Why it exists (owner report 28.10.2026: on one TV box YouTube is dead while everything else works,
 * while third-party clients on the very same subscription work everywhere): the panel's sing-box
 * profile declares only `address: ["172.19.0.1/30"]` on the tun inbound. VPNService.openTun adds an
 * address and a route per family ONLY when the config carries one (VPNService.kt:87-118), so with no
 * IPv6 address the tun never receives `::/0` and every IPv6 socket leaves through the physical
 * interface — outside the tunnel, unproxied, still "green". Android prefers IPv6 when both families
 * answer, so exactly the Google/YouTube domain set that must ride the tunnel can escape it.
 *
 * Third-party clients (v2rayNG, mihomo, NekoBox) build their tun from their OWN defaults, which
 * include an IPv6 address — that is why the same subscription works there. Upstream sing-box-for-
 * android ships the same default in its profile template. An absent IPv6 prefix is therefore a
 * configuration gap, not a deliberate choice.
 *
 * The pinned engine (sing-box v1.14.0-alpha.31) keeps ONE address list for the tun inbound: the
 * per-family keys are documented as deprecated and merged into it, and the engine refuses the WHOLE
 * config while any of them is set (option/tun.go:53-64, protocol/tun/inbound.go:61-67). The engine
 * then splits that list into the families itself and libbox hands the IPv6 part to
 * VPNService.openTun (protocol/tun/inbound.go:80-88, experimental/libbox/tun.go:92-96). So the
 * prefix is APPENDED to the list that is already there — never written as a separate key.
 *
 * Only a recognised shape is touched: no tun inbound, unparseable JSON, a tun without a non-empty
 * `address` array, or a list that already mentions IPv6 all return the input unchanged. Pure string
 * in, string out — the caller validates with Libbox.checkConfig and keeps the original config when
 * the engine refuses.
 */
internal object TunIpv6 {
    /** The address upstream sing-box-for-android puts in its default template. */
    const val DEFAULT_INET6 = "fdfe:dcba:9876::1/126"

    fun ensure(base: String): String {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return base
        val inbounds = (root["inbounds"] as? JsonArray) ?: return base
        var changed = false
        val updated = inbounds.map { element ->
            val inbound = element as? JsonObject ?: return@map element
            if ((inbound["type"] as? JsonPrimitive)?.content != "tun") return@map element
            val address = inbound["address"] as? JsonArray ?: return@map element
            if (address.isEmpty() || address.any { it.isIpv6() }) return@map element
            changed = true
            JsonObject(inbound + ("address" to JsonArray(address + JsonPrimitive(DEFAULT_INET6))))
        }
        if (!changed) return base
        return JsonObject(root + ("inbounds" to JsonArray(updated))).toString()
    }

    private fun JsonElement.isIpv6(): Boolean = ((this as? JsonPrimitive)?.content ?: "").contains(':')
}

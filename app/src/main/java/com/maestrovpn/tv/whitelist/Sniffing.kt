package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Enables traffic sniffing on the tun inbound, the way the third-party clients on the same
 * subscription do by default.
 *
 * Why it exists (task 2026-11-17: on the 32-bit TV boxes — Tanix W2, api 30 — YouTube is black
 * with the VPN on, while phones and Happ on the same subscription work): without "sniff" the
 * engine routes by the dialed IP only, so TLS/QUIC destinations that answer from a different
 * address than the one dialed escape the tunnel. With "sniff": true the engine parses the TLS
 * SNI / HTTP Host of the first packets and matches the route rules against the real destination.
 *
 * Only a recognised shape is touched: no tun inbound or an unparseable JSON return the input
 * unchanged. A tun that already sniffs is left as it is. Pure string in, string out — the caller
 * validates with Libbox.checkConfig and keeps the original config when the engine refuses.
 */
internal object Sniffing {
    fun ensure(base: String): String {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return base
        val inbounds = (root["inbounds"] as? JsonArray) ?: return base
        var changed = false
        val updated = inbounds.map { element ->
            val inbound = element as? JsonObject ?: return@map element
            if ((inbound["type"] as? JsonPrimitive)?.content != "tun") return@map element
            if (inbound["sniff"] == JsonPrimitive(true)) return@map element
            changed = true
            JsonObject(inbound + ("sniff" to JsonPrimitive(true)))
        }
        if (!changed) return base
        return JsonObject(root + ("inbounds" to JsonArray(updated))).toString()
    }
}

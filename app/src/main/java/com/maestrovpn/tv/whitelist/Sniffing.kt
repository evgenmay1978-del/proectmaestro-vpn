package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Turns on protocol sniffing the way the third-party clients on the same subscription do it
 * (task 2026-11-17: on the 32-bit TV boxes — Tanix W2, api 30 — YouTube is black with the VPN on,
 * while phones and Happ on the same subscription work; Happ sniffs http/tls/quic).
 *
 * Since sing-box 1.12 sniffing is a route action, not an inbound switch: the legacy
 * "sniff": true on the tun inbound is still parsed by v1.14.4 but no longer honoured (the field
 * has no reader in the engine). The working shape is a route rule that runs before every routing
 * rule:
 *
 *     { "action": "sniff", "sniffer": ["http", "tls", "quic"] }
 *
 * Without it the engine routes by the dialed IP only, so TLS/QUIC destinations that answer from a
 * different address than the one dialed escape the tunnel. With it the engine parses the TLS SNI,
 * the HTTP Host and the QUIC SNI of the first packets and matches the route rules (and the
 * subscription's domain rules) against the real destination.
 *
 * Only a recognised shape is touched: no route and no inbounds, or an unparseable JSON, return the
 * input unchanged. A route that already has a sniff action is left alone apart from dropping the
 * dead legacy inbound keys. Pure string in, string out — the caller validates with
 * Libbox.checkConfig and keeps the previous config when the engine refuses.
 */
internal object Sniffing {
    private val SNIFFERS = JsonArray(listOf(JsonPrimitive("http"), JsonPrimitive("tls"), JsonPrimitive("quic")))

    private val SNIFF_RULE = JsonObject(
        mapOf(
            "action" to JsonPrimitive("sniff"),
            "sniffer" to SNIFFERS,
        ),
    )

    /** Inbound keys the engine stopped reading in sing-box 1.12. */
    private val LEGACY_INBOUND_KEYS = listOf("sniff", "sniff_override_destination", "sniff_timeout")

    fun ensure(base: String): String {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return base
        val inbounds = root["inbounds"] as? JsonArray
        val route = root["route"] as? JsonObject
        if (inbounds == null && route == null) return base
        var changed = false

        val cleanedInbounds = inbounds?.map { element ->
            val inbound = element as? JsonObject ?: return@map element
            if (LEGACY_INBOUND_KEYS.none { inbound.containsKey(it) }) return@map element
            changed = true
            JsonObject(inbound - LEGACY_INBOUND_KEYS.toSet())
        }

        val rules = (route?.get("rules") as? JsonArray) ?: JsonArray(emptyList())
        val alreadySniffs = rules.any { rule ->
            (rule as? JsonObject)?.get("action") == JsonPrimitive("sniff")
        }
        val newRules = if (alreadySniffs) {
            rules
        } else {
            changed = true
            JsonArray(listOf(SNIFF_RULE) + rules)
        }

        if (!changed) return base
        val newRoute = JsonObject((route ?: JsonObject(emptyMap())) + ("rules" to newRules))
        var updated = root + ("route" to newRoute)
        if (cleanedInbounds != null) {
            updated = updated + ("inbounds" to JsonArray(cleanedInbounds))
        }
        return JsonObject(updated).toString()
    }
}

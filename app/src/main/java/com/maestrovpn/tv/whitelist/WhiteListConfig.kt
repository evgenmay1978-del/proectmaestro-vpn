package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

internal object WhiteListConfig {
    /** Recover the saved ordinary choice; never select direct/block or an injected CDN node. */
    fun ordinaryTag(base: String): String? {
        val root = Json.parseToJsonElement(base) as JsonObject
        val outbounds = (root["outbounds"] as JsonArray).map { it as JsonObject }
        // AWG из обычной подписки sing-box держит в top-level "endpoints" (type "awg"), а не в
        // "outbounds": без этого выбранный awg не попадал в usable, и реконсиляция managed-режима
        // молча откатывала его на "auto" — ядро уходило по VLESS (инцидент 16.10.2026).
        val endpoints = (root["endpoints"] as? JsonArray)?.mapNotNull { it as? JsonObject }.orEmpty()
        fun JsonObject.text(key: String) = (this[key] as? JsonPrimitive)?.content
        if (outbounds.any { it.text("tag")?.startsWith("cdn:") == true }) return null
        val selector = outbounds.singleOrNull { it.text("tag") == "select" && it.text("type") == "selector" } ?: return null
        val choices = (selector["outbounds"] as JsonArray).mapNotNull { (it as? JsonPrimitive)?.content }
        val selectable = outbounds + endpoints
        val usable = choices.filter { tag ->
            selectable.any { it.text("tag") == tag && it.text("type") in setOf(
                "selector", "urltest", "vless", "hysteria2", "naive", "anytls", "trojan", "vmess", "shadowsocks", "wireguard", "awg",
            ) }
        }
        return selector.text("default")?.takeIf { it in usable } ?: "auto".takeIf { it in usable } ?: usable.firstOrNull()
    }

    /**
     * Убирает decoy-пакеты AmneziaWG (i1..i5) из awg-эндпоинтов.
     *
     * Панель выдаёт всем клиентам один и тот же префикс i1, и с ним рукопожатие с сервером
     * AmneziaWG 3.1 не проходит; официальный клиент AmneziaVPN эти поля не отправляет и с тем же
     * конфигом держит туннель. Без i1..i5 наш движок на 3.1 проходит рукопожатие
     * (проверено на телефоне 17.10.2026). Конфиг без awg-эндпоинтов возвращается без изменений.
     */
    fun stripAwgDecoys(base: String): String {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return base
        val endpoints = root["endpoints"] as? JsonArray ?: return base
        val decoys = setOf("i1", "i2", "i3", "i4", "i5")
        fun JsonObject.isAwgWithDecoys() =
            (this["type"] as? JsonPrimitive)?.content == "awg" && keys.any { it in decoys }
        if (endpoints.none { (it as? JsonObject)?.isAwgWithDecoys() == true }) return base
        val cleaned = JsonArray(endpoints.map { element ->
            val endpoint = element as? JsonObject
            if (endpoint?.isAwgWithDecoys() == true) JsonObject(endpoint.filterKeys { it !in decoys }) else element
        })
        return JsonObject(root + ("endpoints" to cleaned)).toString()
    }

    /** Only an explicitly selected CDN session uses this ephemeral variant. */
    fun inject(base: String, route: WhiteListRuntimeRoute, edge: String, port: Int, user: String, pass: String): String {
        require(port in 1024..65_535 && user.isNotBlank() && pass.isNotBlank() && user != pass)
        val root = Json.parseToJsonElement(base) as JsonObject
        val outbounds = root["outbounds"] as JsonArray
        require(outbounds.none { (it as? JsonObject)?.get("tag")?.let { tag -> (tag as? JsonPrimitive)?.content?.startsWith("cdn:") } == true })
        var selectors = 0
        val updated = outbounds.map { element ->
            val outbound = element as JsonObject
            if ((outbound["tag"] as? JsonPrimitive)?.content != "select") return@map element
            require((outbound["type"] as? JsonPrimitive)?.content == "selector")
            selectors++
            val choices = outbound["outbounds"] as JsonArray
            JsonObject(outbound + mapOf(
                "outbounds" to JsonArray(choices + JsonPrimitive(route.tag)),
                "default" to JsonPrimitive(route.tag),
                "interrupt_exist_connections" to JsonPrimitive(true),
            ))
        }
        require(selectors == 1)
        val socks = buildJsonObject {
            put("type", "socks"); put("tag", route.tag); put("server", "127.0.0.1"); put("server_port", port)
            put("version", "5"); put("username", user); put("password", pass)
            put("network", JsonArray(listOf(JsonPrimitive("tcp"), JsonPrimitive("udp"))))
            // UDP stays inside the same authenticated TCP SOCKS association; no UDP listener.
            put("udp_over_tcp", buildJsonObject { put("enabled", true); put("version", 2) })
        }
        return JsonObject(
            root + ("outbounds" to JsonArray(updated + socks)) + ("route" to edgeDirectRoute(root, edge)),
        ).toString()
    }

    /**
     * The XHTTP client runs as a separate process (XhttpProcess) and therefore cannot call
     * VpnService.protect(), so its own connection to the CDN edge would otherwise be captured by
     * the tun and loop back into this same outbound. One DIRECT rule for the resolved edge
     * address, placed above every other rule, keeps that connection outside the tunnel.
     */
    private fun edgeDirectRoute(root: JsonObject, edge: String): JsonObject {
        require(IPV4.matches(edge)) { "CDN edge address must be a literal IPv4" }
        val current = root["route"] as? JsonObject ?: JsonObject(emptyMap())
        val rules = current["rules"] as? JsonArray ?: JsonArray(emptyList())
        val rule = buildJsonObject {
            put("ip_cidr", JsonArray(listOf(JsonPrimitive("$edge/32"))))
            put("action", JsonPrimitive("route"))
            put("outbound", JsonPrimitive("direct"))
        }
        return JsonObject(current + ("rules" to JsonArray(listOf(rule) + rules)))
    }

    private val IPV4 = Regex("^\\d{1,3}(\\.\\d{1,3}){3}$")

    fun selectOrdinary(base: String, tag: String): String {
        require(!tag.startsWith("cdn:"))
        val root = Json.parseToJsonElement(base) as JsonObject
        val outbounds = root["outbounds"] as JsonArray
        var found = false
        val updated = outbounds.map { element ->
            val outbound = element as JsonObject
            if ((outbound["tag"] as? JsonPrimitive)?.content != "select") return@map element
            require((outbound["type"] as? JsonPrimitive)?.content == "selector")
            require((outbound["outbounds"] as JsonArray).any { (it as? JsonPrimitive)?.content == tag })
            found = true
            JsonObject(outbound + ("default" to JsonPrimitive(tag)))
        }
        require(found)
        return JsonObject(root + ("outbounds" to JsonArray(updated))).toString()
    }

    fun payload(route: WhiteListRuntimeRoute, address: String, port: Int, user: String, pass: String): ByteArray =
        buildJsonObject {
            put("schema", 1); put("address", address); put("port", route.port)
            put("server_name", route.serverName); put("host", route.host); put("path", route.path)
            put("client_id", route.clientId); put("encryption", route.encryption)
            put("socks_port", port); put("socks_user", user); put("socks_pass", pass)
        }.toString().toByteArray(Charsets.UTF_8).also { require(it.size <= 16_384) }
}

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
        fun JsonObject.text(key: String) = (this[key] as? JsonPrimitive)?.content
        if (outbounds.any { it.text("tag")?.startsWith("cdn:") == true }) return null
        val selector = outbounds.singleOrNull { it.text("tag") == "select" && it.text("type") == "selector" } ?: return null
        val choices = (selector["outbounds"] as JsonArray).mapNotNull { (it as? JsonPrimitive)?.content }
        val usable = choices.filter { tag ->
            outbounds.any { it.text("tag") == tag && it.text("type") in setOf(
                "selector", "urltest", "vless", "hysteria2", "naive", "anytls", "trojan", "vmess", "shadowsocks", "wireguard",
            ) }
        }
        return selector.text("default")?.takeIf { it in usable } ?: "auto".takeIf { it in usable } ?: usable.firstOrNull()
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

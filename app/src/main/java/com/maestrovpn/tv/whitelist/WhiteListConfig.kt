package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

internal object WhiteListConfig {
    /** Only an explicitly selected CDN session uses this ephemeral variant. */
    fun inject(base: String, route: WhiteListRuntimeRoute, port: Int, user: String, pass: String): String {
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
        return JsonObject(root + ("outbounds" to JsonArray(updated + socks))).toString()
    }

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

package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The applied config must capture IPv6: without an IPv6 address on the tun inbound the app adds no
 * IPv6 route, so IPv6 sockets bypass the tunnel while the UI stays green (owner report 28.10.2026,
 * YouTube dead on one TV box while third-party clients work everywhere on the same subscription).
 */
class TunIpv6Test {
    @Test
    fun addsTheUpstreamDefaultWhenOnlyIpv4IsDeclared() {
        val tuned = TunIpv6.ensure(tunConfig(address = "\"172.19.0.1/30\""))

        assertEquals(TunIpv6.DEFAULT_INET6, tunText(tuned, "inet6_address"))
        assertEquals("172.19.0.1/30", firstAddress(tuned))
    }

    @Test
    fun keepsAnExistingInet6Address() {
        val config = tunConfig(address = "\"172.19.0.1/30\"", extraProperties = "\"inet6_address\": \"fd00::1/64\", ")

        assertEquals(config, TunIpv6.ensure(config))
    }

    @Test
    fun keepsAnIpv6AddressInsideTheAddressList() {
        val config = tunConfig(address = "\"172.19.0.1/30\", \"fd00::1/64\"")

        assertEquals(config, TunIpv6.ensure(config))
    }

    @Test
    fun leavesConfigsWithoutATunInboundAlone() {
        val config = "{\"inbounds\":[{\"type\":\"socks\",\"tag\":\"maestro-probe\",\"listen_port\":1234}]}"

        assertEquals(config, TunIpv6.ensure(config))
    }

    @Test
    fun leavesGarbageAlone() {
        assertEquals("not json", TunIpv6.ensure("not json"))
        assertEquals("{\"inbounds\":{}}", TunIpv6.ensure("{\"inbounds\":{}}"))
    }

    @Test
    fun everyTunInboundIsCoveredAndOthersAreUntouched() {
        val config = "{\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",\"address\":[\"172.19.0.1/30\"]}," +
            "{\"type\":\"mixed\",\"tag\":\"m\",\"listen_port\":2080}]," +
            "\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]}"

        val inbounds = (Json.parseToJsonElement(TunIpv6.ensure(config)) as JsonObject)["inbounds"] as JsonArray
        val elements = inbounds.map { it as JsonObject }

        assertEquals(TunIpv6.DEFAULT_INET6, (elements[0]["inet6_address"] as? JsonPrimitive)?.content)
        assertNull("a non-tun inbound must stay untouched", elements[1]["inet6_address"])
        assertTrue("the rest of the config survives", (elements[0]["address"] as JsonArray).isNotEmpty())
    }

    /** A config shaped like the panel's profile: one tun inbound plus one direct outbound. */
    private fun tunConfig(address: String, extraProperties: String = ""): String =
        "{\"log\":{\"level\":\"warn\"},\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",$extraProperties" +
            "\"address\":[$address],\"auto_route\":true}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]}"

    private fun tunObject(config: String): JsonObject = ((Json.parseToJsonElement(config) as JsonObject)["inbounds"] as JsonArray)
        .map { it as JsonObject }
        .single { (it["type"] as? JsonPrimitive)?.content == "tun" }

    private fun tunText(config: String, key: String): String? = (tunObject(config)[key] as? JsonPrimitive)?.content

    private fun firstAddress(config: String): String =
        ((tunObject(config)["address"] as JsonArray).first() as JsonPrimitive).content
}

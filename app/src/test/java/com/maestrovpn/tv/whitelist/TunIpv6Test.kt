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
 * The applied config must capture IPv6: without an IPv6 prefix on the tun inbound the app adds no
 * IPv6 route, so IPv6 sockets bypass the tunnel while the UI stays green (owner report 28.10.2026,
 * YouTube dead on one TV box while third-party clients work everywhere on the same subscription).
 *
 * The pinned engine keeps a single `address` list on the tun inbound and rejects the whole config
 * while any per-family key is set (sing-box v1.14.0-alpha.31, protocol/tun/inbound.go:61-67), so the
 * prefix has to be APPENDED to that list. Both halves are pinned here: the appended result and the
 * "never a separate key" contract.
 */
class TunIpv6Test {
    @Test
    fun appendsTheIpv6PrefixToTheExistingAddressList() {
        val tuned = TunIpv6.ensure(tunConfig(address = "\"172.19.0.1/30\""))

        assertEquals(listOf("172.19.0.1/30", TunIpv6.DEFAULT_INET6), addressList(tuned))
    }

    @Test
    fun neverAddsASeparateFamilyKey() {
        val tuned = TunIpv6.ensure(tunConfig(address = "\"172.19.0.1/30\""))

        assertNull("a removed per-family key must never reappear", tunObject(tuned)["inet6" + "_address"])
    }

    @Test
    fun keepsAnIpv6AddressInsideTheAddressList() {
        val config = tunConfig(address = "\"172.19.0.1/30\", \"fd00::1/64\"")

        assertEquals(config, TunIpv6.ensure(config))
    }

    @Test
    fun appendingTwiceChangesNothingTheSecondTime() {
        val once = TunIpv6.ensure(tunConfig(address = "\"172.19.0.1/30\""))

        assertEquals(once, TunIpv6.ensure(once))
        assertEquals(listOf("172.19.0.1/30", TunIpv6.DEFAULT_INET6), addressList(once))
    }

    @Test
    fun leavesATunWithoutAnAddressArrayAlone() {
        val config = tunConfig(address = null)

        assertEquals(config, TunIpv6.ensure(config))
    }

    @Test
    fun leavesATunWithAnEmptyAddressArrayAlone() {
        val config = tunConfig(address = "")

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

        val elements = inbounds(TunIpv6.ensure(config))

        assertEquals(listOf("172.19.0.1/30", TunIpv6.DEFAULT_INET6), addressList(TunIpv6.ensure(config)))
        assertEquals(2, (elements[0]["address"] as JsonArray).size)
        assertNull("a non-tun inbound must stay untouched", elements[1]["address"])
    }

    @Test
    fun keepsTheRestOfTheConfigIntact() {
        val tuned = TunIpv6.ensure(tunConfig(address = "\"172.19.0.1/30\""))

        val root = Json.parseToJsonElement(tuned) as JsonObject
        assertEquals("warn", ((root["log"] as JsonObject)["level"] as JsonPrimitive).content)
        assertEquals(1, (root["outbounds"] as JsonArray).size)
        assertEquals("true", (tunObject(tuned)["auto_route"] as JsonPrimitive).content)
        assertTrue("the IPv4 prefix must stay first", addressList(tuned).first() == "172.19.0.1/30")
    }

    /** A config shaped like the panel's profile: one tun inbound plus one direct outbound. */
    private fun tunConfig(address: String?, extraProperties: String = ""): String {
        val addressPart = if (address == null) "" else "\"address\":[$address],"
        return "{\"log\":{\"level\":\"warn\"},\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",$extraProperties" +
            addressPart + "\"auto_route\":true}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]}"
    }

    private fun inbounds(config: String): List<JsonObject> =
        ((Json.parseToJsonElement(config) as JsonObject)["inbounds"] as JsonArray).map { it as JsonObject }

    private fun tunObject(config: String): JsonObject =
        inbounds(config).single { (it["type"] as? JsonPrimitive)?.content == "tun" }

    private fun addressList(config: String): List<String> =
        (tunObject(config)["address"] as JsonArray).map { (it as JsonPrimitive).content }
}

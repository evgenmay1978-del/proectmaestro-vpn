package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Sniffing is what the third-party clients on the same subscription do by default and what the
 * 32-bit TV boxes were missing: without "sniff": true the engine routes by the dialed IP and
 * TLS/QUIC destinations that answer from another address escape the tunnel (task 17.11.2026,
 * YouTube black on Tanix W2 while phones and Happ work).
 */
class SniffingTest {
    @Test fun addsSniffTrueToTheTunInbound() {
        val tuned = Sniffing.ensure(tunConfig())

        assertEquals("true", (tunObject(tuned)["sniff"] as JsonPrimitive).content)
    }

    @Test fun turnsAnExplicitFalseIntoTrue() {
        val tuned = Sniffing.ensure(tunConfig(extraProperties = "\"sniff\":false,"))

        assertEquals("true", (tunObject(tuned)["sniff"] as JsonPrimitive).content)
    }

    @Test fun keepsAnAlreadySniffingConfigUnchanged() {
        val config = tunConfig(extraProperties = "\"sniff\":true,")

        assertEquals(config, Sniffing.ensure(config))
    }

    @Test fun applyingTwiceChangesNothingTheSecondTime() {
        val once = Sniffing.ensure(tunConfig())

        assertEquals(once, Sniffing.ensure(once))
    }

    @Test fun everyTunInboundIsCoveredAndOthersAreUntouched() {
        val config = "{\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",\"address\":[\"172.19.0.1/30\"]}," +
            "{\"type\":\"mixed\",\"tag\":\"m\",\"listen_port\":2080}]}"

        val elements = inbounds(Sniffing.ensure(config))

        assertEquals("true", (elements[0]["sniff"] as JsonPrimitive).content)
        assertNull("a non-tun inbound must stay untouched", elements[1]["sniff"])
    }

    @Test fun leavesConfigsWithoutATunInboundAlone() {
        val config = "{\"inbounds\":[{\"type\":\"socks\",\"tag\":\"maestro-probe\",\"listen_port\":1234}]}"

        assertEquals(config, Sniffing.ensure(config))
    }

    @Test fun leavesGarbageAlone() {
        assertEquals("not json", Sniffing.ensure("not json"))
        assertEquals("{\"inbounds\":{}}", Sniffing.ensure("{\"inbounds\":{}}"))
    }

    @Test fun keepsTheRestOfTheConfigIntact() {
        val tuned = Sniffing.ensure(tunConfig())

        val root = Json.parseToJsonElement(tuned) as JsonObject
        assertEquals("warn", ((root["log"] as JsonObject)["level"] as JsonPrimitive).content)
        assertEquals(1, (root["outbounds"] as JsonArray).size)
        assertEquals("172.19.0.1/30", addressList(tuned).first())
    }

    private fun tunConfig(extraProperties: String = ""): String =
        "{\"log\":{\"level\":\"warn\"},\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",$extraProperties" +
            "\"address\":[\"172.19.0.1/30\"],\"auto_route\":true}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]}"

    private fun inbounds(config: String): List<JsonObject> =
        ((Json.parseToJsonElement(config) as JsonObject)["inbounds"] as JsonArray).map { it as JsonObject }

    private fun tunObject(config: String): JsonObject =
        inbounds(config).single { (it["type"] as? JsonPrimitive)?.content == "tun" }

    private fun addressList(config: String): List<String> =
        (tunObject(config)["address"] as JsonArray).map { (it as JsonPrimitive).content }
}

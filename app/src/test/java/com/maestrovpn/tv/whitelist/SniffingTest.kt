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
 * Sniffing is what the third-party clients on the same subscription do by default (Happ sniffs
 * http/tls/quic) and what the 32-bit TV boxes were missing: since sing-box 1.12 the inbound
 * "sniff": true switch is dead (the field has no reader in the engine), so the working shape is a
 * route rule { "action": "sniff", "sniffer": ["http", "tls", "quic"] } placed before every
 * routing rule. Without it the engine routes by the dialed IP and TLS/QUIC destinations that
 * answer from another address escape the tunnel (task 17.11.2026, YouTube black on Tanix W2 while
 * phones and Happ work).
 */
class SniffingTest {
    @Test fun prependsTheSniffRuleBeforeEveryRoutingRule() {
        val tuned = Sniffing.ensure(tunConfigWithDomainRule())

        val rules = routeRules(tuned)
        assertEquals(2, rules.size)
        assertEquals("sniff", (rules.first()["action"] as JsonPrimitive).content)
        assertEquals(
            "youtube must still be routed by its domain rule",
            "direct",
            (rules[1]["outbound"] as JsonPrimitive).content,
        )
    }

    @Test fun sniffsHttpTlsAndQuicLikeHapp() {
        val tuned = Sniffing.ensure(tunConfigWithDomainRule())

        val sniffer = (routeRules(tuned).first()["sniffer"] as JsonArray).map { it.jsonPrimitive.content }
        assertEquals(listOf("http", "tls", "quic"), sniffer)
    }

    @Test fun createsTheRouteSectionWhenTheConfigHasNone() {
        val tuned = Sniffing.ensure(
            "{\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",\"address\":[\"172.19.0.1/30\"]}]," +
                "\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]}",
        )

        val rule = routeRules(tuned).single()
        assertEquals("sniff", (rule["action"] as JsonPrimitive).content)
        assertNull("a fresh route must not invent a final outbound", rule["outbound"])
    }

    @Test fun dropsTheDeadLegacyInboundKeys() {
        val tuned = Sniffing.ensure(
            tunConfigWithDomainRule(extraInboundProperties = "\"sniff\":true,\"sniff_override_destination\":true,"),
        )

        val tun = tunObject(tuned)
        assertNull("legacy sniff must go", tun["sniff"])
        assertNull("legacy sniff_override_destination must go", tun["sniff_override_destination"])
        assertEquals("true", (tun["auto_route"] as JsonPrimitive).content)
    }

    @Test fun keepsAnExistingSniffActionAndOnlyDropsLegacyKeys() {
        val config = "{\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",\"sniff\":false," +
            "\"address\":[\"172.19.0.1/30\"]}]," +
            "\"route\":{\"rules\":[{\"action\":\"sniff\",\"sniffer\":[\"stun\"]}]}}"
        val tuned = Sniffing.ensure(config)

        assertEquals(1, routeRules(tuned).size)
        val sniffer = (routeRules(tuned).first()["sniffer"] as JsonArray).map { it.jsonPrimitive.content }
        assertEquals("the subscription's own sniff rule must win", listOf("stun"), sniffer)
        assertNull(tunObject(tuned)["sniff"])
    }

    @Test fun applyingTwiceChangesNothingTheSecondTime() {
        val once = Sniffing.ensure(tunConfigWithDomainRule())

        assertEquals(once, Sniffing.ensure(once))
    }

    @Test fun leavesAConfigWithoutInboundsAndRouteAlone() {
        val config = "{\"log\":{\"level\":\"warn\"}}"

        assertEquals(config, Sniffing.ensure(config))
    }

    @Test fun leavesGarbageAlone() {
        assertEquals("not json", Sniffing.ensure("not json"))
        assertEquals("{\"inbounds\":{}}", Sniffing.ensure("{\"inbounds\":{}}"))
    }

    @Test fun keepsTheRestOfTheConfigIntact() {
        val tuned = Sniffing.ensure(tunConfigWithDomainRule())

        val root = Json.parseToJsonElement(tuned) as JsonObject
        assertEquals("warn", ((root["log"] as JsonObject)["level"] as JsonPrimitive).content)
        assertEquals(1, (root["outbounds"] as JsonArray).size)
        assertEquals("172.19.0.1/30", addressList(tuned).first())
    }

    @Test fun doesNotTouchATunInboundWithoutLegacyKeys() {
        val tuned = Sniffing.ensure(tunConfigWithDomainRule())

        assertTrue("auto_route must survive untouched", (tunObject(tuned)["auto_route"] as JsonPrimitive).content.toBoolean())
    }

    private fun tunConfigWithDomainRule(extraInboundProperties: String = ""): String =
        "{\"log\":{\"level\":\"warn\"}," +
            "\"inbounds\":[{\"type\":\"tun\",\"tag\":\"tun-in\",$extraInboundProperties" +
            "\"address\":[\"172.19.0.1/30\"],\"auto_route\":true}]," +
            "\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}]," +
            "\"route\":{\"rules\":[{\"domain_suffix\":[\"googlevideo.com\"],\"outbound\":\"direct\"}]," +
            "\"final\":\"direct\"}}"

    private fun routeRules(config: String): List<JsonObject> {
        val root = Json.parseToJsonElement(config) as JsonObject
        val route = root["route"] as JsonObject
        return (route["rules"] as JsonArray).map { it as JsonObject }
    }

    private fun tunObject(config: String): JsonObject {
        val root = Json.parseToJsonElement(config) as JsonObject
        return (root["inbounds"] as JsonArray)
            .map { it as JsonObject }
            .single { (it["type"] as? JsonPrimitive)?.content == "tun" }
    }

    private fun addressList(config: String): List<String> =
        (tunObject(config)["address"] as JsonArray).map { (it as JsonPrimitive).content }
}

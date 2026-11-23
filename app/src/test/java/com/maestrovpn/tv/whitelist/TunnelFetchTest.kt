package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class TunnelFetchTest {
    @After
    fun clearPort() {
        TunnelFetch.clear()
    }

    // A panel-shaped config: tun inbound, select/auto/direct outbounds, sniff + hijack-dns
    // then panelDirect at position 2, DNS with a google DoT server and a panel → local rule.
    private fun panelConfig(): String = """
        {
          "log": {"level": "warn"},
          "dns": {
            "servers": [
              {"type": "tls", "tag": "google", "server": "8.8.8.8", "detour": "select"},
              {"type": "local", "tag": "local"}
            ],
            "rules": [
              {"domain_suffix": ["wapmixx.ru"], "action": "route", "server": "local"},
              {"rule_set": ["geoip-ru"], "action": "route", "server": "local"}
            ],
            "final": "google"
          },
          "inbounds": [
            {"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30"]}
          ],
          "outbounds": [
            {"type": "selector", "tag": "select", "outbounds": ["auto", "direct"]},
            {"type": "urltest", "tag": "auto", "outbounds": ["direct"]},
            {"type": "direct", "tag": "direct"}
          ],
          "route": {
            "final": "select",
            "rules": [
              {"action": "sniff"},
              {"action": "hijack-dns"},
              {"domain_suffix": ["wapmixx.ru"], "action": "route", "outbound": "direct"},
              {"domain_suffix": ["example.com"], "action": "route", "outbound": "select"}
            ]
          }
        }
    """.trimIndent()

    private fun parse(config: String): JsonObject =
        Json.parseToJsonElement(config) as JsonObject

    private fun JsonObject.inbounds(): JsonArray = this["inbounds"] as JsonArray
    private fun JsonObject.routeRules(): JsonArray = (this["route"] as JsonObject)["rules"] as JsonArray
    private fun JsonObject.dnsRules(): JsonArray = (this["dns"] as JsonObject)["rules"] as JsonArray

    private fun JsonObject.stringField(name: String): String? =
        (this[name] as? JsonPrimitive)?.content

    private fun JsonObject.stringList(name: String): List<String> =
        (this[name] as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.content } ?: emptyList()

    @Test
    fun addsMixedInboundAndRuleDirectlyAbovePanelDirect() {
        val config = panelConfig()
        val patched = TunnelFetch.ensure(config)

        assertNotSame("a patch must produce new bytes", config, patched)
        val root = parse(patched)
        val inbound = root.inbounds().map { it as JsonObject }
            .single { it.stringField("tag") == TunnelFetch.TAG }
        assertEquals("mixed", inbound.stringField("type"))
        assertEquals("127.0.0.1", inbound.stringField("listen"))
        val port = (inbound["listen_port"] as JsonPrimitive).content.toInt()
        assertTrue("port must be a usable loopback port", port in 1..65_535)
        assertEquals("ensure must remember the port for the fetchers", port, TunnelFetch.proxyPort)
        assertTrue(TunnelFetch.isAvailable)

        val rules = root.routeRules().map { it as JsonObject }
        assertEquals("the rule lands at position 2, above panelDirect", TunnelFetch.TAG, rules[2].stringList("inbound").single())
        assertEquals(listOf(TunnelFetch.PANEL_HOST), rules[2].stringList("domain_suffix"))
        assertEquals("route", rules[2].stringField("action"))
        assertEquals("select", rules[2].stringField("outbound"))
        assertEquals("panelDirect stays right below the new rule", "direct", rules[3].stringField("outbound"))
        assertEquals(listOf("wapmixx.ru"), rules[3].stringList("domain_suffix"))
        assertEquals("nothing else in the route moves", 5, rules.size)
        assertEquals("outbounds are untouched", 3, (root["outbounds"] as JsonArray).size)
    }

    @Test
    fun ensureIsIdempotentAndKeepsTheSamePort() {
        val patched = TunnelFetch.ensure(panelConfig())
        val port = TunnelFetch.proxyPort
        val again = TunnelFetch.ensure(patched)

        assertEquals("a second apply must not rewrite the config", patched, again)
        assertEquals("a second apply must keep the same port", port, TunnelFetch.proxyPort)
    }

    @Test
    fun skipsConfigWithoutSelectOutbound() {
        val config = panelConfig().replace(
            "{\"type\": \"selector\", \"tag\": \"select\", \"outbounds\": [\"auto\", \"direct\"]},",
            "{\"type\": \"direct\", \"tag\": \"direct\"},",
        )
        val result = TunnelFetch.ensure(config)

        assertSame("no select outbound means no tunnel route to build", config, result)
        assertEquals(0, TunnelFetch.proxyPort)
    }

    @Test
    fun returnsInputUnchangedWhenUnparseable() {
        val config = "not json"
        assertSame(config, TunnelFetch.ensure(config))
        assertEquals(0, TunnelFetch.proxyPort)
    }

    @Test
    fun ensureDnsRoutesPanelHostViaTunnelDoTFirst() {
        val patched = TunnelFetch.ensureDns(panelConfig())
        val root = parse(patched)
        val rules = root.dnsRules().map { it as JsonObject }

        assertEquals("the inbound-scoped rule goes first", TunnelFetch.TAG, rules[0].stringList("inbound").single())
        assertEquals(listOf(TunnelFetch.PANEL_HOST), rules[0].stringList("domain_suffix"))
        assertEquals(listOf("A", "AAAA"), rules[0].stringList("query_type"))
        assertEquals("route", rules[0].stringField("action"))
        assertEquals("google", rules[0].stringField("server"))
        assertEquals("the panel → local rule stays, now second", "local", rules[1].stringField("server"))
        assertEquals(3, rules.size)
    }

    @Test
    fun ensureDnsIsIdempotentAndSkipsConfigWithoutGoogle() {
        val patched = TunnelFetch.ensureDns(panelConfig())
        assertEquals("a second apply must not rewrite the DNS rules", patched, TunnelFetch.ensureDns(patched))

        val noGoogle = panelConfig().replace(
            "{\"type\": \"tls\", \"tag\": \"google\", \"server\": \"8.8.8.8\", \"detour\": \"select\"},",
            "",
        )
        assertSame("no google server means no DoT to route to", noGoogle, TunnelFetch.ensureDns(noGoogle))
    }

    @Test
    fun clearDropsThePort() {
        TunnelFetch.ensure(panelConfig())
        assertTrue(TunnelFetch.isAvailable)
        TunnelFetch.clear()
        assertEquals(0, TunnelFetch.proxyPort)
        assertTrue(!TunnelFetch.isAvailable)
    }
}

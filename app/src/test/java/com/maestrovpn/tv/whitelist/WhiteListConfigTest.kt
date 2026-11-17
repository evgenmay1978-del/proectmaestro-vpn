package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class WhiteListConfigTest {
    private val route = WhiteListRuntimeRoute("a".repeat(64), "CDN Test", "transport", "release", "preset",
        "cdn.example.com", 443, "cdn.example.com", "cdn.example.com", "/transport",
        "00000000-0000-4000-8000-000000000001", "mlkem768x25519plus.native.0rtt." + "A".repeat(1579))
    private val EDGE = "188.72.111.7"
    private val base = """{"dns":{"strategy":"prefer_ipv4"},"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","ordinary"],"default":"auto"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}]}"""

    @Test fun onlyExplicitSelectorGetsCdnAndOrdinaryAutomaticSetIsUnchanged() {
        val before = Json.parseToJsonElement(base).jsonObject
        val after = Json.parseToJsonElement(WhiteListConfig.inject(base, route, EDGE, 12345, "user", "pass")).jsonObject
        assertEquals(before["dns"], after["dns"])
        val original = before["outbounds"]!!.jsonArray
        val outbounds = after["outbounds"]!!.jsonArray
        assertEquals(original[1], outbounds[1])
        assertEquals(original[2], outbounds[2])
        assertEquals(route.tag, outbounds[0].jsonObject["default"]!!.jsonPrimitive.content)
        assertEquals(listOf("auto", "ordinary", route.tag), outbounds[0].jsonObject["outbounds"]!!.jsonArray.map { it.jsonPrimitive.content })
        val socks = outbounds.last().jsonObject
        assertEquals("127.0.0.1", socks["server"]!!.jsonPrimitive.content)
        assertEquals(listOf("tcp", "udp"), socks["network"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals("user", socks["username"]!!.jsonPrimitive.content)
        assertEquals("pass", socks["password"]!!.jsonPrimitive.content)
        assertTrue(socks["udp_over_tcp"]!!.jsonObject["enabled"]!!.jsonPrimitive.boolean)
        assertEquals(2, socks["udp_over_tcp"]!!.jsonObject["version"]!!.jsonPrimitive.int)
        assertEquals("auto", Json.parseToJsonElement(base).jsonObject["outbounds"]!!.jsonArray[0].jsonObject["default"]!!.jsonPrimitive.content)
    }

    /**
     * The XHTTP client is a separate process and cannot call VpnService.protect(), so the edge
     * address of the selected CDN route must leave through "direct" before any other rule — that
     * is what keeps the child's own connection out of the tun instead of looping into itself.
     */
    @Test fun cdnEdgeAddressIsRoutedDirectAboveEveryOtherRule() {
        val after = Json.parseToJsonElement(WhiteListConfig.inject(base, route, EDGE, 12345, "user", "pass")).jsonObject
        val rule = after["route"]!!.jsonObject["rules"]!!.jsonArray.first().jsonObject
        assertEquals(listOf("$EDGE/32"), rule["ip_cidr"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals("route", rule["action"]!!.jsonPrimitive.content)
        assertEquals("direct", rule["outbound"]!!.jsonPrimitive.content)
        assertTrue(runCatching { WhiteListConfig.inject(base, route, "not-an-ip", 12345, "user", "pass") }.isFailure)
    }

    @Test fun explicitOrdinaryRestoreHasNoNativeOutbound() {
        val restored = Json.parseToJsonElement(WhiteListConfig.selectOrdinary(base, "ordinary")).jsonObject["outbounds"]!!.jsonArray
        assertEquals(3, restored.size)
        assertEquals("ordinary", restored[0].jsonObject["default"]!!.jsonPrimitive.content)
        assertTrue(runCatching { WhiteListConfig.selectOrdinary(base, route.tag) }.isFailure)
        assertTrue(runCatching { WhiteListConfig.selectOrdinary(base, "missing") }.isFailure)
        assertTrue(runCatching { WhiteListConfig.inject(base.replace("\"select\"", "\"other\""), route, EDGE, 12345, "user", "pass") }.isFailure)
    }

    @Test fun nativePayloadUsesPublishedFieldsAndResolvedAddressOnly() {
        val raw = WhiteListConfig.payload(route, "8.8.8.8", 12345, "user", "pass")
        assertTrue(raw.size < 16_384)
        val payload = Json.parseToJsonElement(raw.toString(Charsets.UTF_8)).jsonObject
        assertEquals(11, payload.size)
        assertEquals("8.8.8.8", payload["address"]!!.jsonPrimitive.content)
        assertEquals(route.serverName, payload["server_name"]!!.jsonPrimitive.content)
        assertEquals(route.clientId, payload["client_id"]!!.jsonPrimitive.content)
        assertEquals(route.encryption, payload["encryption"]!!.jsonPrimitive.content)
        assertNull(payload["subscription"])
        assertNull(payload["route_id"])
    }

    /**
     * AWG is removed from the engine (task 17.11.2026): a leftover awg pick in "default" is no
     * longer a usable outbound type, so ordinaryTag falls back exactly as it does for any other
     * unknown tag — to «Авто», never to a dead outbound.
     */
    @Test fun ordinaryTagFallsBackToAutoForAnUnknownDefault() {
        val unknown = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","awg"],"default":"awg"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}]}"""
        assertEquals("auto", WhiteListConfig.ordinaryTag(unknown))
    }

    @Test fun ordinaryTagStillRefusesAnInjectedCdnSession() {
        val withCdn = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","cdn:x"],"default":"cdn:x"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"socks","tag":"cdn:x"}]}"""
        assertNull(WhiteListConfig.ordinaryTag(withCdn))
    }

    /**
     * The engine v1.14.4 has no AWG/AnyTLS/Naive and the app has no wdtt/olcrtc, but the panel
     * still emits those tags for older installs. stripLegacy removes every outbound/endpoint of a
     * removed type or tag and repairs every reference before the config reaches the engine.
     */
    @Test fun stripLegacyRemovesLegacyOutboundsAndEndpointsAndRepairsReferences() {
        val config = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","naive-node","awg","olcrtc"],"default":"awg"},
            {"type":"urltest","tag":"auto","outbounds":["vless-node","naive-node"]},
            {"type":"vless","tag":"vless-node","server":"185.121.12.47","server_port":443},
            {"type":"naive","tag":"naive-node"},
            {"type":"direct","tag":"direct"},
            {"type":"socks","tag":"olcrtc"}],
            "endpoints":[{"type":"awg","tag":"awg"},{"type":"wireguard","tag":"vk-turn"}],
            "route":{"final":"select","rules":[{"outbound":"olcrtc"},{"outbound":"direct"}]}}"""
        val root = Json.parseToJsonElement(WhiteListConfig.stripLegacy(config)).jsonObject
        val outbounds = root["outbounds"]!!.jsonArray.map { it.jsonObject }
        assertEquals(
            listOf("select", "auto", "vless-node", "direct"),
            outbounds.map { it["tag"]!!.jsonPrimitive.content },
        )
        assertEquals(listOf("auto"), outbounds[0]["outbounds"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals("auto", outbounds[0]["default"]!!.jsonPrimitive.content)
        assertEquals(listOf("vless-node"), outbounds[1]["outbounds"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertTrue(root["endpoints"]!!.jsonArray.isEmpty())
        val route = root["route"]!!.jsonObject
        assertEquals(listOf("direct"), route["rules"]!!.jsonArray.map { it.jsonObject["outbound"]!!.jsonPrimitive.content })
    }

    @Test fun stripLegacyKeepsAnEmptiedSelectorStartableWithDirect() {
        val config = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["olcrtc"],"default":"olcrtc"},
            {"type":"socks","tag":"olcrtc"},{"type":"direct","tag":"direct"}]}"""
        val root = Json.parseToJsonElement(WhiteListConfig.stripLegacy(config)).jsonObject
        val select = root["outbounds"]!!.jsonArray.first().jsonObject
        assertEquals(listOf("direct"), select["outbounds"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals("direct", select["default"]!!.jsonPrimitive.content)
    }

    @Test fun stripLegacyLeavesConfigsWithoutLegacyByteForByte() {
        val clean = """{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}"""
        assertEquals(clean, WhiteListConfig.stripLegacy(clean))
        assertEquals("not json", WhiteListConfig.stripLegacy("not json"))
        assertEquals("[]", WhiteListConfig.stripLegacy("[]"))
    }

    /**
     * A CDN pick must not depend on the selector's live choice: every "select" hop of the loaded
     * subscription — both route rules and the final outbound — is retargeted onto the ephemeral
     * CDN outbound, while the edge's DIRECT rule and concrete outbounds stay untouched.
     */
    @Test fun everySelectHopIsRetargetedToTheCdnOutbound() {
        val withRoute = """{"dns":{"strategy":"prefer_ipv4"},"route":{"final":"select","rules":[{"action":"sniff"},{"action":"route","outbound":"select","domain_suffix":["example.com"]}]},"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","ordinary"],"default":"auto"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}]}"""
        val routed = Json.parseToJsonElement(WhiteListConfig.inject(withRoute, route, EDGE, 12345, "user", "pass")).jsonObject["route"]!!.jsonObject
        assertEquals(route.tag, routed["final"]!!.jsonPrimitive.content)
        val rules = routed["rules"]!!.jsonArray.map { it.jsonObject }
        assertEquals("direct", rules.first()["outbound"]!!.jsonPrimitive.content)
        val selectHop = rules.first { it["domain_suffix"] != null }
        assertEquals(route.tag, selectHop["outbound"]!!.jsonPrimitive.content)
        assertTrue(rules.none { (it["outbound"] as? JsonPrimitive)?.content == "select" })
    }
}

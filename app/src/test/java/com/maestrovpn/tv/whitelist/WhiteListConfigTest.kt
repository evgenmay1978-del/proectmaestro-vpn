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
     * AWG from the ordinary subscription sits in the top-level "endpoints" array (type "awg"),
     * not in "outbounds". Managed-mode reconciliation must accept such a pick — otherwise it is
     * silently dropped back to "auto" and the core keeps running a VLESS outbound while the user
     * believes AWG is selected (owner incident, 16.10.2026).
     */
    @Test fun ordinaryTagKeepsAwgEndpointChoice() {
        val withAwg = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","awg"],"default":"awg"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}],"endpoints":[{"type":"awg","tag":"awg"}]}"""
        assertEquals("awg", WhiteListConfig.ordinaryTag(withAwg))
    }

    @Test fun ordinaryTagFallsBackToAutoWhenTheEndpointIsGone() {
        val withoutEndpoint = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","awg"],"default":"awg"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}]}"""
        assertEquals("auto", WhiteListConfig.ordinaryTag(withoutEndpoint))
    }

    @Test fun ordinaryTagIgnoresAnEndpointThatWasNotChosen() {
        val autoChosen = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","awg"],"default":"auto"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}],"endpoints":[{"type":"awg","tag":"awg"}]}"""
        assertEquals("auto", WhiteListConfig.ordinaryTag(autoChosen))
    }

    @Test fun ordinaryTagStillRefusesAnInjectedCdnSession() {
        val withCdn = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","cdn:x"],"default":"cdn:x"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"socks","tag":"cdn:x"}],"endpoints":[{"type":"awg","tag":"awg"}]}"""
        assertNull(WhiteListConfig.ordinaryTag(withCdn))
    }

    @Test fun stripAwgDecoysRemovesOnlyI1ToI5FromAwgEndpoints() {
        val config = """{"outbounds":[{"type":"direct","tag":"direct","i1":"keep"}],"endpoints":[{"type":"awg","tag":"awg","jc":4,"s1":10,"i1":"<b 0xc2>","i2":"x","i5":"y"},{"type":"wireguard","tag":"wg","i1":"keep"}]}"""
        val root = Json.parseToJsonElement(WhiteListConfig.stripAwgDecoys(config)).jsonObject
        val endpoints = root["endpoints"]!!.jsonArray.map { it.jsonObject }
        val awg = endpoints.single { it["tag"]!!.jsonPrimitive.content == "awg" }
        assertNull(awg["i1"]); assertNull(awg["i2"]); assertNull(awg["i5"])
        assertEquals("4", awg["jc"]!!.jsonPrimitive.content)
        assertEquals("10", awg["s1"]!!.jsonPrimitive.content)
        assertEquals("keep", endpoints.single { it["tag"]!!.jsonPrimitive.content == "wg" }["i1"]!!.jsonPrimitive.content)
        assertEquals("keep", root["outbounds"]!!.jsonArray[0].jsonObject["i1"]!!.jsonPrimitive.content)
    }

    @Test fun stripAwgDecoysLeavesConfigsWithoutDecoysByteForByte() {
        val noAwg = """{"outbounds":[{"type":"direct","tag":"direct"}]}"""
        val awgClean = """{"endpoints":[{"type":"awg","tag":"awg","jc":4}]}"""
        assertEquals(noAwg, WhiteListConfig.stripAwgDecoys(noAwg))
        assertEquals(awgClean, WhiteListConfig.stripAwgDecoys(awgClean))
        assertEquals("not json", WhiteListConfig.stripAwgDecoys("not json"))
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

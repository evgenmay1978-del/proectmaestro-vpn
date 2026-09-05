package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class WhiteListConfigTest {
    private val route = WhiteListRuntimeRoute("a".repeat(64), "CDN Test", "transport", "release", "preset",
        "cdn.example.com", 443, "cdn.example.com", "cdn.example.com", "/transport",
        "00000000-0000-4000-8000-000000000001", "mlkem768x25519plus.native.0rtt." + "A".repeat(1579))
    private val base = """{"dns":{"strategy":"prefer_ipv4"},"outbounds":[{"type":"selector","tag":"select","outbounds":["auto","ordinary"],"default":"auto"},{"type":"urltest","tag":"auto","outbounds":["ordinary"]},{"type":"direct","tag":"ordinary"}]}"""

    @Test fun onlyExplicitSelectorGetsCdnAndOrdinaryAutomaticSetIsUnchanged() {
        val before = Json.parseToJsonElement(base).jsonObject
        val after = Json.parseToJsonElement(WhiteListConfig.inject(base, route, 12345, "user", "pass")).jsonObject
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

    @Test fun explicitOrdinaryRestoreHasNoNativeOutbound() {
        val restored = Json.parseToJsonElement(WhiteListConfig.selectOrdinary(base, "ordinary")).jsonObject["outbounds"]!!.jsonArray
        assertEquals(3, restored.size)
        assertEquals("ordinary", restored[0].jsonObject["default"]!!.jsonPrimitive.content)
        assertTrue(runCatching { WhiteListConfig.selectOrdinary(base, route.tag) }.isFailure)
        assertTrue(runCatching { WhiteListConfig.selectOrdinary(base, "missing") }.isFailure)
        assertTrue(runCatching { WhiteListConfig.inject(base.replace("\"select\"", "\"other\""), route, 12345, "user", "pass") }.isFailure)
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
}

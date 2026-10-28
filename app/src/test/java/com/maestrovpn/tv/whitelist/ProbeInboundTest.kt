package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ProbeInboundTest {
    private val base = """
        {"log":{"level":"warn"},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"]}],
         "outbounds":[{"type":"direct","tag":"direct"}]}
    """.trimIndent()

    private fun attached(config: String = base): ProbeInbound.Attached {
        val result = ProbeInbound.attach(config)
        assertNotNull("the probe entry must attach to a normal config", result)
        return result!!
    }

    @Test
    fun addsAnAuthenticatedLoopbackSocksInbound() {
        val attached = attached()
        val root = Json.parseToJsonElement(attached.config) as JsonObject
        val inbounds = root["inbounds"] as JsonArray
        val added = inbounds.map { it as JsonObject }.single { (it["tag"] as JsonPrimitive).content == ProbeInbound.TAG }
        assertEquals("socks", (added["type"] as JsonPrimitive).content)
        assertEquals("127.0.0.1", (added["listen"] as JsonPrimitive).content)
        assertEquals(attached.endpoint.port, (added["listen_port"] as JsonPrimitive).content.toInt())
        val user = (added["users"] as JsonArray).single() as JsonObject
        assertEquals(attached.endpoint.username, (user["username"] as JsonPrimitive).content)
        assertEquals(attached.endpoint.password, (user["password"] as JsonPrimitive).content)
    }

    @Test
    fun keepsEveryExistingInbound() {
        val inbounds = (Json.parseToJsonElement(attached().config) as JsonObject)["inbounds"] as JsonArray
        assertEquals(2, inbounds.size)
        assertEquals("tun-in", ((inbounds.first() as JsonObject)["tag"] as JsonPrimitive).content)
    }

    @Test
    fun credentialsAreRandomAndNeverPrinted() {
        val first = attached()
        val second = attached()
        assertNotEquals(first.endpoint.username, second.endpoint.username)
        assertNotEquals(first.endpoint.password, first.endpoint.username)
        assertTrue(first.endpoint.username.length >= 16 && first.endpoint.password.length >= 16)
        // The credential characters must survive a SOCKS5 user/pass sub-negotiation untouched.
        assertTrue(first.endpoint.username.all { it in "0123456789abcdef" })
        assertEquals("Endpoint(port=" + first.endpoint.port + ")", first.endpoint.toString())
        assertFalse(first.endpoint.toString().contains(first.endpoint.username))
    }

    @Test
    fun refusesToAttachTwice() {
        assertNull(ProbeInbound.attach(attached().config))
    }

    @Test
    fun portIsInsideTheUsableRange() {
        val port = attached().endpoint.port
        assertTrue(port in 1024..65_535)
    }

    @Test
    fun leavesGarbageAlone() {
        assertNull(ProbeInbound.attach("{not json"))
        assertNull(ProbeInbound.attach("""{"outbounds":[]}"""))
    }
}

package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Применяемый конфиг обязан нести gVisor-стек: панель пинит `"stack": "system"`, а на Android без
 * root TCP-трансляция системным стеком не выполняется — в tun приходит только UDP, все TCP-сокеты
 * приложений теряются (замер r689, 18.11: inbound UDP 100 / TCP 0, DNS работает).
 *
 * Здесь закреплены обе половины: подмена на gvisor и «не трогать то, что уже рабочее».
 */
class TunStackTest {
    @Test
    fun replacesTheSystemStackWithGvisor() {
        val tuned = TunStack.ensure(config(stack = "\"system\""))
        assertEquals("gvisor", stackOf(tuned))
        // Everything else about the tun inbound survives the rewrite.
        assertEquals("tun", field(tuned, "type"))
        assertEquals(true, tuned.contains("172.19.0.1/30"))
    }

    @Test
    fun replacesAMissingStackAsWell() {
        assertEquals("gvisor", stackOf(TunStack.ensure(config(stack = null))))
    }

    @Test
    fun keepsGvisorUntouched() {
        val source = config(stack = "\"gvisor\"")
        assertSame(source, TunStack.ensure(source))
    }

    @Test
    fun keepsMixedUntouched() {
        // mixed is system TCP + gvisor UDP: it is a deliberate choice of the panel, not our call.
        val source = config(stack = "\"mixed\"")
        assertSame(source, TunStack.ensure(source))
    }

    @Test
    fun leavesConfigsWithoutATunInboundAlone() {
        val source = "{\"inbounds\":[{\"type\":\"socks\",\"tag\":\"in\"}]}"
        assertSame(source, TunStack.ensure(source))
    }

    @Test
    fun leavesUnparseableJsonAlone() {
        val source = "not json"
        assertSame(source, TunStack.ensure(source))
    }

    @Test
    fun effectiveReportsWhatTheEngineWillSee() {
        assertEquals("gvisor", TunStack.effective(TunStack.ensure(config(stack = "\"system\""))))
        assertEquals("mixed", TunStack.effective(config(stack = "\"mixed\"")))
        assertEquals("-", TunStack.effective("{\"inbounds\":[]}"))
    }

    private fun config(stack: String?): String {
        val tun = buildMap<String, kotlinx.serialization.json.JsonElement> {
            put("type", JsonPrimitive("tun"))
            put("tag", JsonPrimitive("tun-in"))
            put("address", JsonArray(listOf(JsonPrimitive("172.19.0.1/30"))))
            put("auto_route", JsonPrimitive(true))
            put("strict_route", JsonPrimitive(true))
            if (stack != null) put("stack", Json.parseToJsonElement(stack) as JsonPrimitive)
        }
        return JsonObject(mapOf("inbounds" to JsonArray(listOf(JsonObject(tun))))).toString()
    }

    private fun tunInbound(config: String): JsonObject =
        (Json.parseToJsonElement(config) as JsonObject)["inbounds"]!!.let { it as JsonArray }
            .map { it as JsonObject }
            .first { (it["type"] as JsonPrimitive).content == "tun" }

    private fun stackOf(config: String): String =
        (tunInbound(config)["stack"] as? JsonPrimitive)?.content ?: "-"

    private fun field(config: String, name: String): String? {
        assertTrue(config.contains(name))
        return (tunInbound(config)[name] as? JsonPrimitive)?.content
    }
}

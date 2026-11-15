package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * «Авто» must pick among third-party servers only (owner, 15.11.2026: our servers are S1 🇪🇸 and
 * S3 🇳🇱, everything else in the subscription comes from third-party subscriptions), while our
 * servers stay in the manual list.
 */
class AutoSelectPoolTest {
    private fun vless(tag: String, server: String) =
        """{"type":"vless","tag":"$tag","server":"$server","server_port":443,"uuid":"00000000-0000-4000-8000-000000000001"}"""

    /** The shape the S1 shim serves the app on 15.11.2026: our two nodes, JFL, WL, xhttp, AWG. */
    private val production = """
        {"log":{"level":"warn"},
         "endpoints":[{"type":"awg","tag":"awg","address":["10.8.0.2/32"],"private_key":"k",
                       "peers":[{"address":"46.30.41.191","port":51820,"public_key":"p"}]}],
         "outbounds":[
          {"type":"selector","tag":"select","outbounds":["auto","🇪🇸 Испания · VLESS","🇳🇱 Нидерланды · VLESS","awg",
            "JFL · 🇩🇪 Германия","JFL · 🇫🇮 Финляндия","WL · 🇨🇭 ⚡️Вьюга - Швейцария","🇫🇮 Akonit · Финляндия"],"default":"auto"},
          {"type":"urltest","tag":"auto","outbounds":["🇪🇸 Испания · VLESS","🇳🇱 Нидерланды · VLESS","awg",
            "JFL · 🇩🇪 Германия","JFL · 🇫🇮 Финляндия","WL · 🇨🇭 ⚡️Вьюга - Швейцария"],
           "url":"https://www.gstatic.com/generate_204","interval":"3m","tolerance":40,"interrupt_exist_connections":true},
          ${vless("🇪🇸 Испания · VLESS", "185.121.12.47")},
          ${vless("🇳🇱 Нидерланды · VLESS", "46.30.42.151")},
          ${vless("JFL · 🇩🇪 Германия", "de1.jfl.example")},
          ${vless("JFL · 🇫🇮 Финляндия", "203.0.113.10")},
          ${vless("WL · 🇨🇭 ⚡️Вьюга - Швейцария", "198.51.100.7")},
          {"type":"socks","tag":"🇫🇮 Akonit · Финляндия","server":"127.0.0.1","server_port":20801},
          {"type":"direct","tag":"direct"}],
         "route":{"final":"select"}}
    """.trimIndent()

    private fun group(config: String, tag: String): JsonObject =
        Json.parseToJsonElement(config).jsonObject["outbounds"]!!.jsonArray
            .map { it.jsonObject }.single { it["tag"]!!.jsonPrimitive.content == tag }

    private fun members(config: String, tag: String): List<String> =
        group(config, tag)["outbounds"]!!.jsonArray.map { it.jsonPrimitive.content }

    @Test fun ownServersLeaveAutoAndEveryThirdPartyServerStays() {
        val result = AutoSelectPool.exclude(production)
        assertEquals(
            listOf("JFL · 🇩🇪 Германия", "JFL · 🇫🇮 Финляндия", "WL · 🇨🇭 ⚡️Вьюга - Швейцария"),
            members(result.config, "auto"),
        )
        assertEquals(
            mapOf("auto" to listOf("🇪🇸 Испания · VLESS", "🇳🇱 Нидерланды · VLESS", "awg")),
            result.removed,
        )
        assertTrue(result.ownOnly.isEmpty())
    }

    @Test fun manualListAndEverythingElseIsUntouched() {
        val result = AutoSelectPool.exclude(production)
        val before = Json.parseToJsonElement(production).jsonObject
        val after = Json.parseToJsonElement(result.config).jsonObject
        assertEquals(group(production, "select"), group(result.config, "select"))
        assertEquals(before["endpoints"], after["endpoints"])
        assertEquals(before["route"], after["route"])
        assertEquals(before["log"], after["log"])
        // Probe settings of «Авто» survive; only its member list changed.
        val auto = group(result.config, "auto")
        assertEquals("40", auto["tolerance"]!!.jsonPrimitive.content)
        assertEquals("3m", auto["interval"]!!.jsonPrimitive.content)
        assertEquals("true", auto["interrupt_exist_connections"]!!.jsonPrimitive.content)
        // Every outbound is still defined: nothing a manual pick or a rule points at disappears.
        assertEquals(before["outbounds"]!!.jsonArray.size, after["outbounds"]!!.jsonArray.size)
    }

    @Test fun ownServerIsRecognisedByItsAddressWhateverItIsCalled() {
        val renamed = """{"outbounds":[
            {"type":"urltest","tag":"auto","outbounds":["node-a","node-b","node-c","JFL · 🇳🇱 Нидерланды"]},
            ${vless("node-a", "185.121.12.47")},
            ${vless("node-b", "WAPMIXX.RU.")},
            ${vless("node-c", "203.0.113.5")},
            ${vless("JFL · 🇳🇱 Нидерланды", "46.30.41.191")}]}"""
        // A tag never proves a node is third-party: S3's address makes even a «JFL ·» tag ours.
        assertEquals(listOf("node-c"), members(AutoSelectPool.exclude(renamed).config, "auto"))
    }

    @Test fun panelAndShimNamesOfOurNodesAreOursEvenWithANewAddress() {
        listOf(
            "🇪🇸 MaestroVPN VLESS", "🇳🇱 MaestroVPN VLESS S3", "🇪🇸 Испания · VLESS", "🇳🇱 Нидерланды · VLESS",
            "🇮🇹 Италия · AnyTLS", "🇪🇸 Испания · S1", "vless", "vless-s3", "awg", "cdn:a1b2", "БС/Yandex Москва",
        ).forEach { assertTrue(it, OwnServers.isOwnTag(it)) }
        listOf(
            "JFL · 🇪🇸 Испания", "WL · 🇳🇱 Нидерланды · VLESS", "xhttp:fi-1", "🇫🇮 Akonit · Финляндия",
            "🇩🇪 Германия · VLESS", "auto", "", null,
        ).forEach { assertFalse(it.toString(), OwnServers.isOwnTag(it)) }
    }

    @Test fun everyUrltestGroupIsCleanedNotOnlyAuto() {
        val config = """{"outbounds":[
            {"type":"selector","tag":"select","outbounds":["auto","fast"]},
            {"type":"urltest","tag":"auto","outbounds":["own","third"]},
            {"type":"urltest","tag":"fast","outbounds":["third","own"]},
            ${vless("own", "185.121.12.47")},
            ${vless("third", "203.0.113.9")}]}"""
        val result = AutoSelectPool.exclude(config)
        assertEquals(listOf("third"), members(result.config, "auto"))
        assertEquals(listOf("third"), members(result.config, "fast"))
        assertEquals(listOf("auto", "fast"), members(result.config, "select"))
    }

    @Test fun nestedGroupOfOurServersCannotSmuggleThemIntoAuto() {
        val config = """{"outbounds":[
            {"type":"urltest","tag":"auto","outbounds":["ours","mixed","third"]},
            {"type":"selector","tag":"ours","outbounds":["es","nl"]},
            {"type":"selector","tag":"mixed","outbounds":["es","third"]},
            ${vless("es", "185.121.12.47")},
            ${vless("nl", "46.30.41.191")},
            ${vless("third", "203.0.113.9")}]}"""
        // A group of nothing but our servers is ours; a manual group with a third-party server in
        // it is not trimmed, because that would change the user's manual choice inside it.
        assertEquals(listOf("mixed", "third"), members(AutoSelectPool.exclude(config).config, "auto"))
    }

    @Test fun groupWithoutAnyThirdPartyServerIsKeptSoTheEngineStillStarts() {
        val config = """{"outbounds":[
            {"type":"urltest","tag":"auto","outbounds":["es","nl"]},
            ${vless("es", "185.121.12.47")},
            ${vless("nl", "46.30.41.191")}]}"""
        val result = AutoSelectPool.exclude(config)
        assertSame(config, result.config)
        assertEquals(listOf("auto"), result.ownOnly)
        assertTrue(result.removed.isEmpty())
    }

    @Test fun configWithoutOurServersInAutoIsPassedThroughByteForByte() {
        val clean = AutoSelectPool.exclude(production).config
        val again = AutoSelectPool.exclude(clean)
        assertSame(clean, again.config)
        assertTrue(again.removed.isEmpty())
        listOf("", "not json", "[]", """{"outbounds":"x"}""", """{"dns":{}}""").forEach {
            assertSame(it, AutoSelectPool.exclude(it).config)
        }
    }

    @Test fun ordinaryFallbackPicksAThirdPartyServerBeforeOurs() {
        val config = """{"outbounds":[
            {"type":"selector","tag":"select","outbounds":["es","third"]},
            ${vless("es", "185.121.12.47")},
            ${vless("third", "203.0.113.9")}]}"""
        assertEquals("third", WhiteListConfig.ordinaryTag(config))
        val onlyOurs = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["es"]},${vless("es", "185.121.12.47")}]}"""
        assertEquals("es", WhiteListConfig.ordinaryTag(onlyOurs))
        val withAuto = """{"outbounds":[{"type":"selector","tag":"select","outbounds":["es","auto"]},
            {"type":"urltest","tag":"auto","outbounds":["es"]},${vless("es", "185.121.12.47")}]}"""
        assertEquals("auto", WhiteListConfig.ordinaryTag(withAuto))
    }
}

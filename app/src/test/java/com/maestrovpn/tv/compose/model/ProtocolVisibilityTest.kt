package com.maestrovpn.tv.compose.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ProtocolVisibilityTest {
    @Test
    fun deferredTransportAliasesAreHiddenWithoutFilteringOtherServers() {
        // Only WebRTC stays deferred: the legacy transports (vk-turn/wdtt/olcrtc) are removed from
        // the engine config by WhiteListConfig.stripLegacy, so the UI no longer hides them here.
        val hidden = listOf("WEBRTC", "webrtc", "  WebRTC  ")
        hidden.forEach { assertFalse(it, isProtocolVisibleInUi(it)) }
        val ordinary = listOf("auto", "vless", "vless-s4", "hysteria2", "vk-turn", "WDTT", "olcrtc", "server-wdtt-backup")
        assertEquals(ordinary, visibleProtocolTags(hidden + ordinary))
    }

    @Test
    fun hiddenActiveProtocolNeverFallsBackToAnUntrueAutoLabel() {
        assertNull(visibleActiveProtocol("WEBRTC", "auto"))
        assertNull(visibleActiveProtocol(null, "WEBRTC"))
        assertEquals("vk-turn", visibleActiveProtocol("vk-turn", "auto"))
        assertEquals("vless", visibleActiveProtocol(null, "vless"))
        assertEquals("auto", visibleActiveProtocol("", "auto"))
        assertEquals("vless-s3", visibleActiveProtocol("vless-s3", "auto"))
    }

    @Test
    fun groupProjectionHidesItemsHeadingsAndSelectionWithoutChangingRuntimeGroups() {
        val source = listOf(
            group("select", "webrtc", "auto", "webrtc", "server-a"),
            group("auto", "server-a", "server-a", "vless-s4"),
        )

        assertEquals(
            listOf(group("select", "", "auto", "server-a"), group("auto", "server-a", "server-a", "vless-s4")),
            visibleProtocolGroups(source),
        )
        assertEquals("webrtc", source.first().selected)
        assertEquals(listOf("auto", "webrtc", "server-a"), source.first().items.map { it.tag })
        assertEquals(emptyList<Group>(), visibleProtocolGroups(listOf(group("select", "webrtc", "webrtc"))))
    }

    @Test
    fun manualAndPendingSelectionsRejectHiddenGroupOrItem() {
        assertFalse(isProtocolSelectionAllowed("select", "webrtc"))
        assertFalse(isProtocolSelectionAllowed("select", "WebRTC"))
        assertFalse(isProtocolSelectionAllowed("webrtc", "relay-a"))
        assertTrue(isProtocolSelectionAllowed("select", "auto"))
        assertTrue(isProtocolSelectionAllowed("vless", "server-a"))
    }


    @Test
    fun jflServersFoldIntoOneTileAndLeaveCdnOffTheTvGrid() {
        val tags = listOf(
            "auto",
            "🇪🇸 MaestroVPN VLESS",
            "🇮🇹 Италия · VLESS",
            "cdn:xhttp-a",
            "JFL · 🇩🇪 Германия",
            "JFL · 🇳🇱 Нидерланды",
            "JFL · 🇸🇪 Швеция",
        )
        assertTrue(isJflServerTag("JFL · 🇩🇪 Германия"))
        assertFalse(isJflServerTag("auto"))
        assertFalse(isJflServerTag(null))
        assertEquals(3, jflServerCount(tags))
        assertEquals(
            listOf("JFL · 🇩🇪 Германия", "JFL · 🇳🇱 Нидерланды", "JFL · 🇸🇪 Швеция"),
            jflServerTags(tags),
        )
        // The JFL tile sits right after «Авто» so the 8-tile grid can never cut it off (15.11.2026).
        assertEquals(
            listOf("auto", "jfl", "🇪🇸 MaestroVPN VLESS", "🇮🇹 Италия · VLESS"),
            tvHomeProtocolTags(tags),
        )
    }

    @Test
    fun pingLabelFormatsOnlyMeasuredLatency() {
        assertEquals("42 мс", pingLabel(42))
        assertEquals("120 мс", pingLabel(120))
        assertNull(pingLabel(0))
        assertNull(pingLabel(-1))
        assertNull(pingLabel(null))
    }
    private fun group(tag: String, selected: String, vararg items: String) = Group(
        tag = tag,
        type = "selector",
        selectable = true,
        selected = selected,
        isExpand = false,
        items = items.map { GroupItem(it, "socks", 0L, 0) },
    )
}

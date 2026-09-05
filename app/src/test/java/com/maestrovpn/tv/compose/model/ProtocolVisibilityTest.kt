package com.maestrovpn.tv.compose.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ProtocolVisibilityTest {
    @Test
    fun deferredTransportAliasesAreHiddenWithoutFilteringOtherServers() {
        val hidden = listOf("vk-turn", "WDTT", "olcrtc", "olcRTC", "OLC RTC", "WEBRTC", "  Vk-Turn  ")
        hidden.forEach { assertFalse(it, isProtocolVisibleInUi(it)) }
        val ordinary = listOf("auto", "vless", "vless-s4", "hysteria2", "naive", "anytls", "awg", "server-wdtt-backup")
        assertEquals(ordinary, visibleProtocolTags(hidden + ordinary))
    }

    @Test
    fun hiddenActiveProtocolNeverFallsBackToAnUntrueAutoLabel() {
        assertNull(visibleActiveProtocol("vk-turn", "auto"))
        assertNull(visibleActiveProtocol("olcrtc", "vless"))
        assertNull(visibleActiveProtocol(null, "WEBRTC"))
        assertEquals("vless", visibleActiveProtocol(null, "vless"))
        assertEquals("auto", visibleActiveProtocol("", "auto"))
        assertEquals("vless-s3", visibleActiveProtocol("vless-s3", "auto"))
    }

    @Test
    fun groupProjectionHidesItemsHeadingsAndSelectionWithoutChangingRuntimeGroups() {
        val source = listOf(
            group("select", "vk-turn", "auto", "vk-turn", "olcrtc", "server-a"),
            group("olcrtc", "relay-a", "relay-a"),
            group("auto", "server-a", "server-a", "vless-s4"),
        )

        assertEquals(
            listOf(group("select", "", "auto", "server-a"), group("auto", "server-a", "server-a", "vless-s4")),
            visibleProtocolGroups(source),
        )
        assertEquals("vk-turn", source.first().selected)
        assertEquals(listOf("auto", "vk-turn", "olcrtc", "server-a"), source.first().items.map { it.tag })
        assertEquals(emptyList<Group>(), visibleProtocolGroups(listOf(group("select", "olcrtc", "olcrtc"))))
    }

    @Test
    fun manualAndPendingSelectionsRejectHiddenGroupOrItem() {
        assertFalse(isProtocolSelectionAllowed("select", "vk-turn"))
        assertFalse(isProtocolSelectionAllowed("select", "WebRTC"))
        assertFalse(isProtocolSelectionAllowed("olcrtc", "relay-a"))
        assertTrue(isProtocolSelectionAllowed("select", "auto"))
        assertTrue(isProtocolSelectionAllowed("vless", "server-a"))
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

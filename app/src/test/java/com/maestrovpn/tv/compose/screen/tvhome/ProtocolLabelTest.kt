package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

class ProtocolLabelTest {
    @Test
    fun vkTurnUsesShortMobileCopy() {
        assertEquals("VK", protocolLabel("vk-turn"))
        assertEquals("через VK", protocolBadge("vk-turn"))
    }
}

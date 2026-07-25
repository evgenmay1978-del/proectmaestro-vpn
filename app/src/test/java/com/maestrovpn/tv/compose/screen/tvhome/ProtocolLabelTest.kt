package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

class ProtocolLabelTest {
    /**
     * The chip label was deliberately changed from "VK" to "WDTT" in 34d9250
     * ("fix(wdtt): label mobile selector clearly", 2026-07-15) and this test kept asserting the
     * old copy. Nothing caught it: no workflow ran the unit tests at all until 2026-07-25, so the
     * suite sat red in the dark for ten days. The badge under the chip still carries the
     * user-facing "через VK", which is what actually explains the route to a customer.
     */
    @Test
    fun vkTurnUsesShortMobileCopy() {
        assertEquals("WDTT", protocolLabel("vk-turn"))
        assertEquals("через VK", protocolBadge("vk-turn"))
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.R
import org.junit.Assert.assertEquals
import org.junit.Test

class PhoneHomeControlDeckContractTest {
    @Test
    fun contactRowUsesMeasuredPlatesAndBrandAssets() {
        assertEquals(
            listOf(
                R.drawable.contact_telegram,
                R.drawable.contact_max,
                R.drawable.contact_whatsapp,
            ),
            homeContactSpecs.map { it.iconRes },
        )
        assertEquals(listOf("Telegram", "МАКС", "WhatsApp"), homeContactSpecs.map { it.label })
        assertEquals(listOf("telegram", "max", "whatsapp"), homeContactSpecs.map { it.tag })
        assertEquals(listOf(34.1f, 144.8f, 255.5f), homeContactSpecs.map { it.leftDp })
        assertEquals(listOf(134.5f, 245.2f, 355.7f), homeContactSpecs.map { it.rightDp })
        assertEquals(496f, HOME_CONTACT_TOP_DP, 0f)
        assertEquals(560f, HOME_CONTACT_BOTTOM_DP, 0f)
        assertEquals(26f, HOME_CONTACT_ICON_DP, 0f)
        assertEquals(10.5f, HOME_CONTACT_LABEL_SP, 0f)
    }

    @Test
    fun protocolLineCannotSayDisconnectedWhileConnecting() {
        assertEquals(
            "Подключение: VLESS",
            homeActiveProtocolLine(
                connected = false,
                connecting = true,
                activeProtocol = "vless",
                selected = "vless",
            ),
        )
        assertEquals(
            "Подключён: VLESS",
            homeActiveProtocolLine(true, false, "vless", "vless"),
        )
        assertEquals(
            "Отключён: VLESS",
            homeActiveProtocolLine(false, false, "vless", "vless"),
        )
    }

    @Test
    fun referenceButtonKeepsVisualHeightSeparateFromTouchTarget() {
        val layout = phoneHomeReferenceLayout(390f, 844f)

        assertEquals(38f, homeButtonVisualHeight(layout.phone), 0f)
        assertEquals(48f, layout.minimumInteractiveHeight, 0f)
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Состав протокольного меню ТВ. Тесты дуги старой телефонной деки (PhoneHomeControlDeck) удалены
 * вместе с ней: дека не вызывалась ни из одного живого экрана (Home телефона = PhoneDashboard).
 */
class PhoneHomeProtocolOrderTest {
    @Test
    fun tvMenuHidesOnlyDeferredTransportAliases() {
        // vk-turn/olcrtc/wdtt must not reach the menu: the engine strips them, but the UI groups are
        // built from the raw subscription payload and used to show an "olcrtc" tile (20.11.2026).
        assertEquals(
            listOf("auto", "vless-s3", "awg"),
            visibleTvProtocols(listOf("auto", "vk-turn", "olcrtc", "vless-s3", "awg")),
        )
        assertEquals(
            listOf("auto", "vless-s3"),
            visibleTvProtocols(listOf("auto", "webrtc", "vless-s3")),
        )
        assertEquals(listOf("auto", "vless", "hysteria2", "vless-s3"), visibleTvProtocols(emptyList()))
    }
}

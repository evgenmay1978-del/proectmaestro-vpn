package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Состав протокольного меню ТВ. Тесты дуги старой телефонной деки (PhoneHomeControlDeck) удалены
 * вместе с ней: дека не вызывалась ни из одного живого экрана (Home телефона = PhoneDashboard).
 */
class PhoneHomeProtocolOrderTest {
    @Test
    fun tvMenuKeepsOnlyTheWebRtcAliasHidden() {
        // vk-turn/olcrtc/awg never reach this list in production: the engine config is stripped by
        // WhiteListConfig.stripLegacy before it starts, so the UI no longer hides them here.
        assertEquals(listOf("auto", "vk-turn", "olcrtc", "vless-s3", "awg"), visibleTvProtocols(listOf("auto", "vk-turn", "olcrtc", "vless-s3", "awg")))
        assertEquals(listOf("auto", "vless-s3"), visibleTvProtocols(listOf("auto", "webrtc", "vless-s3")))
        assertEquals(listOf("auto", "vless", "hysteria2", "vless-s3"), visibleTvProtocols(emptyList()))
    }
}

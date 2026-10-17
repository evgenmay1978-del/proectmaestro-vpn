package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Состав протокольного меню ТВ. Тесты дуги старой телефонной деки (PhoneHomeControlDeck) удалены
 * вместе с ней: дека не вызывалась ни из одного живого экрана (Home телефона = PhoneDashboard).
 */
class PhoneHomeProtocolOrderTest {
    @Test
    fun tvMenuHasNoDeferredTeaserAndKeepsOrdinaryServerEntries() {
        assertEquals(listOf("auto", "vless-s3", "awg"), visibleTvProtocols(listOf("auto", "vk-turn", "olcrtc", "vless-s3", "awg")))
        assertEquals(emptyList<String>(), visibleTvProtocols(listOf("vk-turn", "olcrtc")))
        assertEquals(listOf("auto", "vless", "hysteria2", "naive", "anytls", "vless-s3", "awg"), visibleTvProtocols(emptyList()))
    }
}

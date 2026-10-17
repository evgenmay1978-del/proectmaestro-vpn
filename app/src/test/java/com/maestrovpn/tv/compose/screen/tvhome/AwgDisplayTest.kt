package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** AmneziaWG приходит endpoint-ом с голым тегом "awg"; в UI он не должен выглядеть сырым тегом. */
class AwgDisplayTest {
    @Test fun serverRowNamesAmneziaWgInsteadOfTheRawTag() {
        assertEquals(AWG_SERVER_NAME to AWG_SERVER_DETAIL, phoneServerTitle("awg", null))
        assertEquals(AWG_SERVER_NAME to AWG_SERVER_DETAIL, phoneServerTitle(" AWG ", null))
    }

    @Test fun runtimeLabelStillWinsForCdnAndOtherTagsStayAsBefore() {
        assertEquals("Испания" to "VLESS", phoneServerTitle("vless", null))
        assertEquals("Германия" to "VLESS", phoneServerTitle("vless-s4", null))
        assertEquals("Автовыбор сервера" to "Обычный VPN", phoneServerTitle("auto", null))
        assertEquals("server-x" to "Обычный VPN", phoneServerTitle("server-x", null))
    }

    @Test fun chipsAndTvShowAmneziaWg() {
        assertEquals("AmneziaWG", protocolLabel("awg"))
        assertEquals("Маскировка трафика", protocolBadge("awg"))
        assertTrue(visibleTvProtocols(listOf("auto", "awg")).contains("awg"))
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * NeuroProtocol (AIVPN) lives in the TV protocol grid as one more tile, and only for accounts the
 * panel actually serves a key to (/info features.aivpn): everyone else would tap into
 * "Подписка отклонила запрос".
 */
class AivpnTileTest {
    @Test fun tileIsHiddenUntilThePanelAdvertisesAivpn() {
        assertFalse(visibleTvProtocols(listOf("auto", "vless")).contains(AIVPN_TV_TAG))
        assertFalse(visibleTvProtocols(listOf("auto", "vless"), aivpnAvailable = false).contains(AIVPN_TV_TAG))
    }

    @Test fun tileJoinsTheGridAfterTheOrdinaryProtocols() {
        assertEquals(listOf("auto", "vless", AIVPN_TV_TAG), visibleTvProtocols(listOf("auto", "vless"), aivpnAvailable = true))
        assertTrue(visibleTvProtocols(emptyList(), aivpnAvailable = true).contains(AIVPN_TV_TAG))
    }

    @Test fun fullGridStillKeepsTheTileWithinEightSlots() {
        val many = (1..9).map { "p$it" }
        val shown = visibleTvProtocols(many, aivpnAvailable = true)
        assertEquals(8, shown.size)
        assertEquals(AIVPN_TV_TAG, shown.last())
        assertEquals(8, visibleTvProtocols(many).size)
    }

    @Test fun infoFeatureFlagIsReadStrictly() {
        assertTrue(infoAdvertisesAivpn("""{"login":"a","features":{"aivpn":true},"aivpn":{"key":"aivpn://x"}}"""))
        assertFalse(infoAdvertisesAivpn("""{"login":"a"}"""))
        assertFalse(infoAdvertisesAivpn("""{"features":{"aivpn":false}}"""))
        assertFalse(infoAdvertisesAivpn("""{"features":{"aivpn":"true"}}"""))
        assertFalse(infoAdvertisesAivpn("""{"features":[1]}"""))
        assertFalse(infoAdvertisesAivpn("not json"))
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.BuildConfig
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PhoneCdnPurchaseLinkTest {
    private val base = BuildConfig.BACKEND_URL.trimEnd('/')
    private val account = 2L to 7L

    @Test fun selectedSubscriptionDeterminesBotAccount() {
        val token = "b".repeat(32)
        assertEquals("https://t.me/MaestroSecureVPN_bot?start=maestro_$token",
            phoneCdnPurchaseLink("$base/sub/$token?device=local&platform=phone", account, account))
        val other = "a".repeat(32)
        assertEquals("https://t.me/MaestroSecureVPN_bot?start=maestro_$other",
            phoneCdnPurchaseLink("$base/sub/$other", 1L to 3L, 1L to 3L))
    }

    @Test fun changedSelectionOrRevisionCancelsPurchaseHandoff() {
        val subscription = "$base/sub/${"b".repeat(32)}"
        assertNull(phoneCdnPurchaseLink(subscription, account, 1L to 7L))
        assertNull(phoneCdnPurchaseLink(subscription, account, 2L to 8L))
        assertNull(phoneCdnPurchaseLink(subscription, -1L to 0L, -1L to 0L))
    }

    @Test fun invalidProfileNeverFallsBackToAnUnboundBot() {
        for (url in listOf(null, "", "http://wapmixx.ru/sub/token", "https://other.invalid/sub/token",
            "$base/sub/", "$base/sub/token/info", "$base/sub/token#fragment", "$base/sub/${"a".repeat(57)}",
            base.replace("https://", "https://user@") + "/sub/token")) {
            assertNull(phoneCdnPurchaseLink(url, account, account))
        }
    }
}

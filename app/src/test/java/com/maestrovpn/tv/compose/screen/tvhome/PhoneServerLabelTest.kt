package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

/** Phone server rows name the server and its transport, never «внешний сервер» (owner, 15.11.2026). */
class PhoneServerLabelTest {
    @Test fun thirdPartyRowsReadLikeOurOwn() {
        assertEquals("Германия" to "VPN", phoneServerTitle("JFL · 🇩🇪 Германия", null))
        assertEquals("Швейцария" to "VPN", phoneServerTitle("WL · 🇨🇭 ⚡️Вьюга - Швейцария", null))
        assertEquals("Испания" to "VLESS", phoneServerTitle("🇪🇸 Испания · VLESS", null))
        listOf("JFL · 🇩🇪 Германия", "WL · 🇨🇭 ⚡️Вьюга - Швейцария", "xhttp:fi-1", "🇫🇮 Akonit · Финляндия").forEach { tag ->
            val (name, detail) = phoneServerTitle(tag, null)
            assertFalse("$tag → $name / $detail", (name + detail).contains("нешн"))
        }
    }

    @Test fun cdnRowsKeepTheirLabel() {
        assertEquals("Москва" to "CDN · XHTTP", phoneServerTitle("cdn:a1", "БС/Yandex Москва"))
    }
}

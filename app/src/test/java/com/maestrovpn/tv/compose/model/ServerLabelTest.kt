package com.maestrovpn.tv.compose.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Test

/** One label for a server on every screen; where it comes from is never printed. */
class ServerLabelTest {
    @Test fun jflServerShowsCountryAndTransport() {
        assertEquals(ServerLabel("🇩🇪", "Германия", "VLESS"), serverLabel("JFL · 🇩🇪 Германия", "vless"))
        assertEquals(ServerLabel("🇺🇸", "США · Нью-Йорк", "VLESS"), serverLabel("JFL · 🇺🇸 США · Нью-Йорк", "vless"))
        // Without a flag the row keeps the name and gets the globe badge.
        assertEquals(ServerLabel("", "Резерв", "VPN"), serverLabel("JFL · Резерв"))
    }

    @Test fun whitelistServerDropsThePanelPictographs() {
        assertEquals(ServerLabel("🇪🇪", "Эстония", "VLESS"), serverLabel("WL · 🇪🇪 🌀 Раю - Эстония ⚡", "vless"))
        assertEquals(ServerLabel("🇩🇪", "Германия", "Hysteria2"), serverLabel("WL · 🇩🇪 Яук – Германия", "hysteria2"))
    }

    @Test fun xhttpNodeDropsTheProviderBrand() {
        assertEquals(
            ServerLabel("🇫🇮", "Финляндия", "VLESS · XHTTP"),
            serverLabel("🇫🇮 Akonit · Финляндия", "socks", subXhttp = true),
        )
        assertEquals(
            ServerLabel("🇫🇮", "Финляндия", "VLESS · XHTTP"),
            serverLabel("🇫🇮 ⚡️Вьюга - Финляндия", null, subXhttp = true),
        )
    }

    @Test fun ourNodesKeepTheirCountryWithoutRepeatingItInTheTransport() {
        assertEquals(ServerLabel("🇪🇸", "Испания", "VLESS"), serverLabel("🇪🇸 Испания · VLESS", "vless"))
        assertEquals(ServerLabel("🇳🇱", "Нидерланды", "VLESS"), serverLabel("🇳🇱 Нидерланды · VLESS"))
        assertEquals(ServerLabel("🇳🇱", "Нидерланды", "VLESS S3"), serverLabel("🇳🇱 MaestroVPN VLESS S3"))
        assertEquals(ServerLabel("🇪🇸", "Испания", "VLESS"), serverLabel("🇪🇸 MaestroVPN VLESS", "vless"))
    }

    @Test fun serviceKeysAreLeftToEachScreen() {
        listOf("auto", "vless", "vless-s3", "cdn:a1b2", "", null).forEach {
            assertNull(it.toString(), serverLabel(it))
        }
    }

    @Test fun noLabelSaysWhereTheServerComesFrom() {
        val tags = listOf(
            "JFL · 🇩🇪 Германия" to "vless", "WL · 🇨🇭 ⚡️Вьюга - Швейцария" to "vless",
            "🇫🇮 Akonit · Финляндия" to "socks", "JFL · Резерв" to null,
        )
        for ((tag, type) in tags) {
            val label = serverLabel(tag, type, subXhttp = tag.contains("Akonit"))!!
            for (text in listOf(label.name, label.detail)) {
                assertFalse(text, text.contains("нешн", ignoreCase = true))
                assertFalse(text, text.startsWith("JFL") || text.startsWith("WL"))
            }
        }
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.compose.model.JFL_GROUP_TAG
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The TV grid holds 8 tiles. Since the subscription carries more servers than that (10 xhttp nodes
 * from 14.11.2026) the JFL tile, appended last, was cut off and the JFL servers could not be
 * reached on the TV at all (owner's report, 15.11.2026).
 */
class TvGridTest {
    private val subscription = listOf("auto", "🇪🇸 Испания · VLESS", "🇳🇱 Нидерланды · VLESS", "WL · 🇨🇭 Вьюга - Швейцария") +
        (1..10).map { "🇫🇮 Akonit · Узел $it" } +
        (1..42).map { "JFL · 🇩🇪 Германия $it" } + listOf("cdn:a1")

    @Test fun jflTileIsShownWhateverTheSubscriptionSize() {
        listOf(false, true).forEach { aivpn ->
            val grid = tvProtocolGrid(subscription, aivpn)
            assertEquals(8, grid.tiles.size)
            assertEquals(listOf("auto", JFL_GROUP_TAG), grid.tiles.take(2))
            if (aivpn) assertEquals(AIVPN_TV_TAG, grid.tiles.last())
        }
    }

    @Test fun serversThatDoNotFitStayReachableBehindOneTile() {
        val grid = tvProtocolGrid(subscription)
        assertEquals(TV_MORE_TAG, grid.tiles.last())
        val reachable = grid.tiles.filterNot { it == TV_MORE_TAG || it == JFL_GROUP_TAG } + grid.overflow
        // Every ordinary server is either a tile or in the «Ещё серверы» picker; JFL folds into its
        // own tile, CDN and the hidden transports stay off the TV grid as before.
        assertEquals(subscription.filterNot { it.startsWith("JFL · ") || it.startsWith("cdn:") }, reachable)
        assertTrue(grid.overflow.none { it.startsWith("JFL · ") })
    }

    @Test fun smallProfileHasNoOverflowTile() {
        val grid = tvProtocolGrid(listOf("auto", "vless", "JFL · 🇩🇪 Германия"))
        assertEquals(listOf("auto", JFL_GROUP_TAG, "vless"), grid.tiles)
        assertTrue(grid.overflow.isEmpty())
    }
}

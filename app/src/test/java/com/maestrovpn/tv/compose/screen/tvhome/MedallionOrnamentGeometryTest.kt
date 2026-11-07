package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.hypot

class MedallionOrnamentGeometryTest {
    // Те же доли, что в ApprovedMobileArtwork.kt (CARVED_MEDALLION_*).
    private val aspect = 720f / 712f
    private val diameter = 570f / 712f
    private val left = 72f / 712f
    private val top = 5f / 720f

    private fun layout(heroWidthPx: Float, captionPx: Float): OrnamentLayout {
        val h = heroWidthPx * aspect
        return medallionOrnamentLayout(
            width = heroWidthPx,
            height = h,
            medallionLeft = heroWidthPx * left,
            medallionTop = h * top,
            medallionSize = heroWidthPx * diameter,
            bottomLimit = h - captionPx,
            margin = 3f,
        )
    }

    // 220/300/360 dp при 2.75x — минимальный, средний и максимальный размер героя.
    private val cases = listOf(605f to 190f, 825f to 190f, 990f to 190f, 990f to 260f)

    @Test
    fun guidesAreSymmetricAroundMedallionCentre() = cases.forEach { (w, cap) ->
        val l = layout(w, cap)
        assertEquals(l.cx - l.clipLeft, l.clipRight - l.cx, 0.01f)
        assertEquals(l.cy - l.clipTop, l.clipBottom - l.cy, 0.01f)
        assertEquals(l.topSpikeEnd, l.bottomSpikeEnd, 0.001f)
        assertTrue(l.guideEnd > l.guideStart)
        assertTrue(l.cx + l.guideEnd <= l.clipRight)
        assertTrue(l.cx - l.guideEnd >= l.clipLeft)
    }

    @Test
    fun raysAreMirrorSymmetricOnBothAxes() = cases.forEach { (w, cap) ->
        val l = layout(w, cap)
        // Каждому лучу есть зеркальный по вертикальной оси (лево ↔ право).
        l.rays.forEach { ray ->
            val mirrored = l.rays.any { m ->
                kotlin.math.abs((2 * l.cx - m.x0) - ray.x0) < 0.05f && kotlin.math.abs(m.y0 - ray.y0) < 0.05f &&
                    kotlin.math.abs((2 * l.cx - m.x1) - ray.x1) < 0.05f && kotlin.math.abs(m.y1 - ray.y1) < 0.05f
            }
            val flipped = l.rays.any { m ->
                kotlin.math.abs(m.x0 - ray.x0) < 0.05f && kotlin.math.abs((2 * l.cy - m.y0) - ray.y0) < 0.05f &&
                    kotlin.math.abs(m.x1 - ray.x1) < 0.05f && kotlin.math.abs((2 * l.cy - m.y1) - ray.y1) < 0.05f
            }
            assertTrue("ray without left/right mirror: $ray", mirrored)
            assertTrue("ray without top/bottom mirror: $ray", flipped)
        }
    }

    @Test
    fun nothingTouchesDiscOrGemPlates() = cases.forEach { (w, cap) ->
        val l = layout(w, cap)
        val gemPlates = 402f / 512f * l.r // плашки камней в disc_off
        assertTrue(l.guideStart > gemPlates)
        assertTrue(l.spikeStart > gemPlates)
        l.rays.forEach { assertTrue(hypot(it.x0 - l.cx, it.y0 - l.cy) > gemPlates) }
        assertTrue(ORNAMENT_CLEAR * l.r > gemPlates)
    }

    @Test
    fun nothingLeavesHeroOrReachesCaption() = cases.forEach { (w, cap) ->
        val l = layout(w, cap)
        val h = w * aspect
        assertTrue(l.clipBottom <= h - cap)
        l.rays.forEach {
            for ((x, y) in listOf(it.x0 to it.y0, it.x1 to it.y1)) {
                assertTrue(x >= l.clipLeft && x <= l.clipRight)
                assertTrue(y >= l.clipTop && y <= l.clipBottom)
            }
        }
        assertTrue(l.cy - l.topSpikeEnd >= l.clipTop)
        assertTrue(l.cy + l.bottomSpikeEnd <= l.clipBottom)
    }

    @Test
    fun rayCountIsStable() {
        val l = layout(990f, 190f)
        assertTrue(l.rays.size in 40..RAY_COUNT)
    }
}

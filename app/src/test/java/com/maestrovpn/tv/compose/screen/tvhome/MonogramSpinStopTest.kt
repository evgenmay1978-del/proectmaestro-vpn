package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Буква останавливается только в анфас (кадр 0): на 180° видна зеркальная задняя сторона, и
 * остановка там дала бы скачок света при переходе в статику (16.11.2026).
 */
class MonogramSpinStopTest {
    @Test
    fun stopsOnTheNextFrontFrameAhead() {
        assertEquals(0f, spinStopTarget(0f), 0f)
        assertEquals(72f, spinStopTarget(0.5f), 0f)
        assertEquals(72f, spinStopTarget(35.9f), 0f)
        assertEquals(72f, spinStopTarget(36f), 0f)
        assertEquals(72f, spinStopTarget(71.99f), 0f)
    }

    @Test
    fun neverWaitsMoreThanOneTurnAndAlwaysLandsOnTheFront() {
        var p = 0f
        while (p < 72f) {
            val target = spinStopTarget(p)
            val wait = target - p
            assertTrue("position $p waits $wait frames", wait >= 0f && wait <= 72f)
            assertEquals(0f, target % 72f, 0f)
            p += 0.37f
        }
    }
}

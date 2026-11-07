package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MonogramSpinStopTest {
    @Test
    fun stopsOnTheNextFrontalFrameAhead() {
        assertEquals(0f, spinStopTarget(0f), 0f)
        assertEquals(36f, spinStopTarget(0.5f), 0f)
        assertEquals(36f, spinStopTarget(35.9f), 0f)
        assertEquals(36f, spinStopTarget(36f), 0f)
        assertEquals(72f, spinStopTarget(36.1f), 0f)
        assertEquals(72f, spinStopTarget(71.99f), 0f)
    }

    @Test
    fun neverWaitsMoreThanHalfATurn() {
        var p = 0f
        while (p < 72f) {
            val wait = spinStopTarget(p) - p
            assertTrue("position $p waits $wait frames", wait >= 0f && wait <= 36f)
            p += 0.37f
        }
    }
}

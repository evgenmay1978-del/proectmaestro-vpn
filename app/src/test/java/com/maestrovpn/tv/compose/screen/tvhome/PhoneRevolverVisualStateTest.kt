package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PhoneRevolverVisualStateTest {
    @Test
    fun centeredRowIsFlatFullSizeAndOpaque() {
        val state = revolverVisualState(0f)

        assertEquals(0f, state.rotationX, 0.0001f)
        assertEquals(1f, state.scale, 0.0001f)
        assertEquals(1f, state.alpha, 0.0001f)
        assertEquals(0f, state.translationY, 0.0001f)
    }

    @Test
    fun rowsAboveAndBelowTiltInOppositeDirections() {
        val above = revolverVisualState(-0.75f)
        val below = revolverVisualState(0.75f)

        assertTrue(above.rotationX < 0f)
        assertTrue(below.rotationX > 0f)
        assertEquals(above.rotationX, -below.rotationX, 0.0001f)
    }

    @Test
    fun edgeRowsShrinkFadeAndMoveOutward() {
        val above = revolverVisualState(-1f)
        val below = revolverVisualState(1f)

        assertTrue(above.scale < 1f)
        assertTrue(above.alpha < 1f)
        assertTrue(above.translationY < 0f)
        assertTrue(below.scale < 1f)
        assertTrue(below.alpha < 1f)
        assertTrue(below.translationY > 0f)
    }
}

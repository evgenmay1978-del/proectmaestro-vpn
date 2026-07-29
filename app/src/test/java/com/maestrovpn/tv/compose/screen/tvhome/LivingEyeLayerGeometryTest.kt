package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class LivingEyeLayerGeometryTest {
    @Test
    fun actualStateSourceRectangleMapsInsideBronzeInset() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val bounds = fit.stateBounds

        assertEquals(26f, bounds.left, 0.001f)
        assertEquals(93.04494f, bounds.top, 0.001f)
        assertEquals(494f, bounds.right, 0.001f)
        assertEquals(426.95505f, bounds.bottom, 0.001f)
        assertEquals(260f, bounds.centerX, 0.001f)
        assertEquals(260f, bounds.centerY, 0.001f)
        assertEquals(0.5258427f, fit.scale, 0.000001f)
        assertEquals(-94.94382f, fit.translationX, 0.001f)
        assertEquals(-298.70786f, fit.translationY, 0.001f)
        assertEquals(fit.mapSourceLengthX(100f), fit.mapSourceLengthY(100f), 0.000001f)
        assertTrue(bounds.left > 0f)
        assertTrue(bounds.right < 520f)
    }

    @Test
    fun transformedStateRectangleCoversEntireStaticOpenEyeAperture() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val transformedApertureBounds = fit.mapSourceBounds(
            left = 388f,
            top = 957f,
            right = 957f,
            bottom = 1174f,
        )
        val staticApertureBounds = LivingEyeLayerBounds(
            left = 75.36049f,
            top = 202.12036f,
            right = 435.09302f,
            bottom = 339.31186f,
            scale = 520f / 822.5f,
        )

        assertEquals(109.083145f, transformedApertureBounds.left, 0.001f)
        assertEquals(204.52359f, transformedApertureBounds.top, 0.001f)
        assertEquals(408.28763f, transformedApertureBounds.right, 0.001f)
        assertEquals(318.63147f, transformedApertureBounds.bottom, 0.001f)
        assertTrue(fit.stateBounds.left <= staticApertureBounds.left)
        assertTrue(fit.stateBounds.top <= staticApertureBounds.top)
        assertTrue(fit.stateBounds.right >= staticApertureBounds.right)
        assertTrue(fit.stateBounds.bottom >= staticApertureBounds.bottom)
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LivingEyeLayerGeometryTest {

    @Test
    fun anatomyUsesOneOwnerApprovedScaleAndOffset() {
        val fit = fitLivingEyeLayer(520f, 520f)
        val aperture = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f).bounds

        assertEquals(0.6954407f, fit.scale, 0.000001f)
        assertEquals(-41.8241f, fit.stateBounds.left, 0.001f)
        assertEquals(54.4917f, fit.stateBounds.top, 0.001f)
        assertEquals(577.1182f, fit.stateBounds.right, 0.001f)
        assertEquals(496.0965f, fit.stateBounds.bottom, 0.001f)
        assertEquals(-201.7754f, fit.translationX, 0.001f)
        assertEquals(-463.6117f, fit.translationY, 0.001f)
        assertEquals(15.8203f, aperture.left, 0.001f)
        assertEquals(185.7728f, aperture.top, 0.001f)
        assertEquals(504.1797f, aperture.right, 0.001f)
        assertEquals(333.8217f, aperture.bottom, 0.001f)
        assertEquals(0.939153f, (aperture.right - aperture.left) / 520f, 0.00001f)
        assertEquals(0.284709f, (aperture.bottom - aperture.top) / 520f, 0.00001f)
    }
    @Test
    fun transformedStateRectangleCoversEntireStaticOpenEyeAperture() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val transformedApertureBounds = fit.mapSourceBounds(
            left = 312.889f,
            top = 933.774f,
            right = 1015.119f,
            bottom = 1146.659f,
        )
        assertEquals(fit.mapSourceLengthX(100f), fit.mapSourceLengthY(100f), 0.000001f)

        // Один uniform transform должен оставлять апертуру внутри прямоугольника состояния.
        assertTrue(fit.stateBounds.left <= transformedApertureBounds.left)
        assertTrue(fit.stateBounds.top <= transformedApertureBounds.top)
        assertTrue(fit.stateBounds.right >= transformedApertureBounds.right)
        assertTrue(fit.stateBounds.bottom >= transformedApertureBounds.bottom)
    }

    @Test
    fun integrationProfileKeepsRegistrationAndDefinesUniformContactSeam() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val profile = livingEyeIntegrationProfile(width = 520f, height = 520f)

        assertEquals(fit.scale, profile.fitScale, 0.000001f)
        assertEquals(fit.stateBounds, profile.stateBounds)
        assertEquals(3f, profile.contactSeamWidthPx, 0.001f)
        assertEquals(0.18f, profile.contactSeamAlpha, 0f)
        assertTrue(profile.contactSeamWidthPx < livingEyeBronzeInset(520f, 520f))
    }

    @Test
    fun fullClosureRevealsRegisteredSurroundAndDisablesAnatomyAndGlow() {
        val open = livingEyeRenderPolicy(closure = 0f)
        val justOpen = livingEyeRenderPolicy(closure = 0.998f)
        val threshold = livingEyeRenderPolicy(closure = 0.999f)
        val closed = livingEyeRenderPolicy(closure = 1f)

        assertTrue(open.eyeLayersEnabled)
        assertTrue(open.glowEnabled)
        assertTrue(justOpen.eyeLayersEnabled)
        assertTrue(justOpen.glowEnabled)

        assertFalse(threshold.eyeLayersEnabled)
        assertFalse(threshold.glowEnabled)

        assertFalse(closed.eyeLayersEnabled)
        assertFalse(closed.glowEnabled)
    }

    @Test
    fun apertureUsesOneCommonGridAndClosesToZeroHeight() {
        val fit = fitLivingEyeLayer(520f, 520f)
        val open = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f)
        val half = livingEyeApertureContour(fit, closure = 0.5f, seamOverlapPx = 0f)
        val closed = livingEyeApertureContour(fit, closure = 1f, seamOverlapPx = 0f)

        assertEquals(open.upper.size, open.lower.size)
        assertEquals(open.upper.map { it.x }, open.lower.map { it.x })
        assertEquals(open.upper.map { it.x }, half.upper.map { it.x })
        assertEquals(open.upper.map { it.x }, half.lower.map { it.x })
        assertEquals(open.upper.map { it.x }, closed.upper.map { it.x })
        assertEquals(open.upper.map { it.x }, closed.lower.map { it.x })

        open.upper.indices.forEach { index ->
            val openHeight = open.lower[index].y - open.upper[index].y
            val halfHeight = half.lower[index].y - half.upper[index].y
            val closedHeight = closed.lower[index].y - closed.upper[index].y
            assertEquals(openHeight * 0.5f, halfHeight, 0.001f)
            assertEquals(0f, closedHeight, 0.001f)
        }
    }

    @Test
    fun lidsCloseOntoOriginalGreenFoldWithFixedCorners() {
        val fit = fitLivingEyeLayer(520f, 520f)
        val open = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f)
        val closed = livingEyeApertureContour(fit, closure = 1f, seamOverlapPx = 0f)
        val sampleX = fit.mapSourceX(664.004f)
        val closedUpper = closed.upper.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val closedLower = closed.lower.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val expectedSeamY = fit.mapSourceY(1111.664f)

        assertEquals(expectedSeamY, closedUpper.y, 0.001f)
        assertEquals(expectedSeamY, closedLower.y, 0.001f)
        assertEquals(open.upper.first(), closed.upper.first())
        assertEquals(open.upper.last(), closed.upper.last())
        assertEquals(open.lower.first(), closed.lower.first())
        assertEquals(open.lower.last(), closed.lower.last())
    }
}

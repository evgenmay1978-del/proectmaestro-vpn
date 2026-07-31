package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class Mobile4DSceneModelTest {
    @Test
    fun centreTiltUsesOnlyCentreLight() {
        assertEquals(
            Mobile4DLightMix(Mobile4DLightSide.Right, 1f, 0f),
            mobile4DLightMix(0f, Mobile4DLightSide.Right),
        )
    }

    @Test
    fun fullLeftTiltUsesOnlyLeftLight() {
        assertEquals(
            Mobile4DLightMix(Mobile4DLightSide.Left, 0f, 1f),
            mobile4DLightMix(-1f, Mobile4DLightSide.Right),
        )
    }

    @Test
    fun fullRightTiltUsesOnlyRightLight() {
        assertEquals(
            Mobile4DLightMix(Mobile4DLightSide.Right, 0f, 1f),
            mobile4DLightMix(1f, Mobile4DLightSide.Left),
        )
    }

    @Test
    fun lightTiltClampsBeyondItsRange() {
        assertEquals(
            Mobile4DLightMix(Mobile4DLightSide.Left, 0f, 1f),
            mobile4DLightMix(-4f, Mobile4DLightSide.Right),
        )
        assertEquals(
            Mobile4DLightMix(Mobile4DLightSide.Right, 0f, 1f),
            mobile4DLightMix(4f, Mobile4DLightSide.Left),
        )
    }

    @Test
    fun oppositeTiltsHaveSymmetricWeights() {
        val left = mobile4DLightMix(-0.35f, Mobile4DLightSide.Right)
        val right = mobile4DLightMix(0.35f, Mobile4DLightSide.Left)

        assertEquals(Mobile4DLightSide.Left, left.activeSide)
        assertEquals(Mobile4DLightSide.Right, right.activeSide)
        assertEquals(left.centerWeight, right.centerWeight, 0.0001f)
        assertEquals(left.sideWeight, right.sideWeight, 0.0001f)
    }

    @Test
    fun lightWeightsAlwaysSumToOneWithOnlyOneSideActive() {
        listOf(-1f, -0.35f, 0f, 0.35f, 1f).forEach { tilt ->
            val mix = mobile4DLightMix(tilt, Mobile4DLightSide.Right)

            assertEquals(1f, mix.centerWeight + mix.sideWeight, 0.0001f)
            assertTrue(mix.leftWeight == 0f || mix.rightWeight == 0f)
        }
    }

    @Test
    fun cropMappingKeepsMedallionOnTheApprovedAnchor() {
        val layout = mobile4DSceneLayout(width = 390f, height = 844f)

        assertEquals(196.6f, layout.medallionCenterX, 0.2f)
        assertEquals(325.3f, layout.medallionCenterY, 0.2f)
        assertEquals(119f, layout.medallionRadiusX, 0.2f)
        assertEquals(119f, layout.medallionRadiusY, 0.2f)
    }

    @Test
    fun landscapeCropUsesTheExactContentScaleCropTransform() {
        val layout = mobile4DSceneLayout(width = 844f, height = 390f)

        assertEquals(844f / 2160f, layout.scale, 0.0001f)
        assertEquals(0f, layout.translationX, 0.0001f)
        assertEquals(-717.4f, layout.translationY, 0.2f)
        assertEquals(425.5f, layout.medallionCenterX, 0.2f)
        assertEquals(-13.8f, layout.medallionCenterY, 0.2f)
    }

    @Test
    fun smallJitterAroundNeutralDoesNotSwitchTheActiveSide() {
        var side = Mobile4DLightSide.Right

        listOf(-0.05f, 0.04f, -0.1f, 0.08f).forEach { tilt ->
            side = mobile4DActiveLightSide(tilt, side)
            assertEquals(Mobile4DLightSide.Right, side)
        }
    }

    @Test
    fun deliberateCrossThresholdTiltSwitchesTheActiveSide() {
        assertEquals(
            Mobile4DLightSide.Left,
            mobile4DActiveLightSide(-0.2f, Mobile4DLightSide.Right),
        )
        assertEquals(
            Mobile4DLightSide.Right,
            mobile4DActiveLightSide(0.2f, Mobile4DLightSide.Left),
        )
    }

    @Test
    fun parallaxOffsetsUseTheApprovedLayerDepths() {
        assertEquals(
            Mobile4DParallaxOffset(0.5f, -0.5f),
            mobile4DParallaxOffset(Mobile4DParallaxLayer.Wood, 1f, -1f),
        )
        assertEquals(
            Mobile4DParallaxOffset(1.5f, -1.5f),
            mobile4DParallaxOffset(Mobile4DParallaxLayer.Frame, 1f, -1f),
        )
        assertEquals(
            Mobile4DParallaxOffset(2.5f, -2.5f),
            mobile4DParallaxOffset(Mobile4DParallaxLayer.Cartouche, 1f, -1f),
        )
        assertEquals(
            Mobile4DParallaxOffset(3.5f, -3.5f),
            mobile4DParallaxOffset(Mobile4DParallaxLayer.Vines, 1f, -1f),
        )
        assertEquals(
            Mobile4DParallaxOffset(5f, -5f),
            mobile4DParallaxOffset(Mobile4DParallaxLayer.RingAndEye, 1f, -1f),
        )
    }

    @Test
    fun disconnectedEyeIsClosed() {
        assertEquals(Mobile4DEyeState.Disconnected, mobile4DEyeState(connected = false, connecting = false))
    }

    @Test
    fun connectingEyeIsHalfOpen() {
        assertEquals(Mobile4DEyeState.Connecting, mobile4DEyeState(connected = false, connecting = true))
    }

    @Test
    fun connectedEyeIsOpenOnlyWhenNotConnecting() {
        assertEquals(Mobile4DEyeState.Connected, mobile4DEyeState(connected = true, connecting = false))
        assertEquals(Mobile4DEyeState.Connecting, mobile4DEyeState(connected = true, connecting = true))
    }
}

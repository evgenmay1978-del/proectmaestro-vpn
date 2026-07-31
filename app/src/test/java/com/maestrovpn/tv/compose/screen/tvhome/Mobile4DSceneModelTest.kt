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

    @Test
    fun displayRotationRemapsTiltAxesExactlyOnce() {
        val portrait = Mobile4DTiltVector(x = 0.25f, y = -0.75f)

        assertEquals(portrait, mobile4DRemapForDisplayRotation(portrait, Mobile4DDisplayRotation.Rotation0))
        assertEquals(
            Mobile4DTiltVector(x = 0.75f, y = 0.25f),
            mobile4DRemapForDisplayRotation(portrait, Mobile4DDisplayRotation.Rotation90),
        )
        assertEquals(
            Mobile4DTiltVector(x = -0.25f, y = 0.75f),
            mobile4DRemapForDisplayRotation(portrait, Mobile4DDisplayRotation.Rotation180),
        )
        assertEquals(
            Mobile4DTiltVector(x = -0.75f, y = -0.25f),
            mobile4DRemapForDisplayRotation(portrait, Mobile4DDisplayRotation.Rotation270),
        )
    }

    @Test
    fun physicalTiltClampsAtTwelveDegreesAndNormalizes() {
        assertEquals(-1f, mobile4DNormalizeTiltDegrees(-40f), 0.0001f)
        assertEquals(-1f, mobile4DNormalizeTiltDegrees(-12f), 0.0001f)
        assertEquals(1f, mobile4DNormalizeTiltDegrees(12f), 0.0001f)
        assertEquals(1f, mobile4DNormalizeTiltDegrees(40f), 0.0001f)
        assertEquals(5.5f / 11.5f, mobile4DNormalizeTiltDegrees(6f), 0.0001f)
    }

    @Test
    fun physicalTiltDeadZoneRemovesNeutralSensorJitter() {
        assertEquals(0f, mobile4DNormalizeTiltDegrees(-0.5f), 0.0001f)
        assertEquals(0f, mobile4DNormalizeTiltDegrees(0f), 0.0001f)
        assertEquals(0f, mobile4DNormalizeTiltDegrees(0.5f), 0.0001f)
        assertTrue(mobile4DNormalizeTiltDegrees(0.6f) > 0f)
    }

    @Test
    fun lowPassUsesElapsedTimeInsteadOfSensorEventCount() {
        val oneHundredMilliseconds = mobile4DLowPass(
            previous = Mobile4DTiltVector.Zero,
            target = Mobile4DTiltVector(1f, -1f),
            elapsedMillis = 100L,
        )
        val firstHalf = mobile4DLowPass(
            previous = Mobile4DTiltVector.Zero,
            target = Mobile4DTiltVector(1f, -1f),
            elapsedMillis = 50L,
        )
        val twoFiftyMillisecondSteps = mobile4DLowPass(
            previous = firstHalf,
            target = Mobile4DTiltVector(1f, -1f),
            elapsedMillis = 50L,
        )

        assertEquals(oneHundredMilliseconds.x, twoFiftyMillisecondSteps.x, 0.0001f)
        assertEquals(oneHundredMilliseconds.y, twoFiftyMillisecondSteps.y, 0.0001f)
        assertEquals(
            oneHundredMilliseconds,
            mobile4DLowPass(oneHundredMilliseconds, Mobile4DTiltVector.Zero, 0L),
        )
    }

    @Test
    fun targetWidthUsesManifestBucketAndCaps() {
        assertEquals(1088, mobile4DTargetWidthBucket(viewportWidthPx = 1081, isLowRamDevice = false))
        assertEquals(1620, mobile4DTargetWidthBucket(viewportWidthPx = 2200, isLowRamDevice = false))
        assertEquals(1080, mobile4DTargetWidthBucket(viewportWidthPx = 2200, isLowRamDevice = true))
    }

    @Test
    fun memoryPolicyKeepsThreeLightsOnlyInsideManifestBudget() {
        val roomy = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 512,
            isLowRamDevice = false,
            relightingEnabled = true,
            sceneMode = Mobile4DSceneMode.Home,
        )
        val constrained = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 256,
            isLowRamDevice = false,
            relightingEnabled = true,
            sceneMode = Mobile4DSceneMode.Home,
        )

        assertEquals(Mobile4DAssetRetention.AllLights, roomy.retention)
        assertEquals(1620, roomy.targetWidthPx)
        assertTrue(roomy.estimatedResidentBytes <= roomy.decodedArtBudgetBytes)
        assertEquals(Mobile4DAssetRetention.CentreAndActiveSide, constrained.retention)
        assertEquals(1536, constrained.targetWidthPx)
        assertTrue(constrained.estimatedResidentBytes <= constrained.decodedArtBudgetBytes)
    }

    @Test
    fun lowRamReducedMotionAndInternalScreensUseLightweightPolicies() {
        val lowRam = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 128,
            isLowRamDevice = true,
            relightingEnabled = true,
            sceneMode = Mobile4DSceneMode.Home,
        )
        val reducedMotion = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 256,
            isLowRamDevice = false,
            relightingEnabled = false,
            sceneMode = Mobile4DSceneMode.Home,
        )
        val internal = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 512,
            isLowRamDevice = false,
            relightingEnabled = true,
            sceneMode = Mobile4DSceneMode.Internal,
        )

        assertEquals(Mobile4DAssetRetention.CentreOnly, lowRam.retention)
        assertTrue(lowRam.targetWidthPx <= Mobile4DGeneratedAssets.lowRamMaximumTargetWidthPx)
        assertEquals(Mobile4DAssetRetention.CentreOnly, reducedMotion.retention)
        assertEquals(Mobile4DAssetRetention.None, internal.retention)
        assertEquals(0, internal.targetWidthPx)
        assertEquals(0L, internal.estimatedResidentBytes)
    }

    @Test
    fun memoryPolicyDisablesHomeArtWhenEvenMinimumBucketExceedsBudget() {
        val noArtBudget = mobile4DAssetMemoryPolicy(
            viewportWidthPx = 1620,
            memoryClassMiB = 8,
            isLowRamDevice = false,
            relightingEnabled = true,
            sceneMode = Mobile4DSceneMode.Home,
        )

        assertEquals(Mobile4DAssetRetention.None, noArtBudget.retention)
        assertEquals(0, noArtBudget.targetWidthPx)
        assertEquals(0L, noArtBudget.estimatedResidentBytes)
        assertTrue(noArtBudget.estimatedResidentBytes <= noArtBudget.decodedArtBudgetBytes)
    }
}

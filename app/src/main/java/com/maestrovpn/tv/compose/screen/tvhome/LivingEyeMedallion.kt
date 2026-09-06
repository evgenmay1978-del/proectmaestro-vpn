package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationVector1D
import androidx.compose.animation.core.FastOutLinearInEasing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.res.imageResource
import com.maestrovpn.tv.R
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.exp
import kotlin.math.ln
import kotlin.math.roundToLong
import kotlin.math.sqrt
import kotlin.random.Random

/**
 * The owner's reference form with the original emerald grain and gold veins.
 * Blink, connection, gaze, touch and pupil clocks are unchanged. One registered
 * green texture forms both moving lids; there is no second baked slit beneath it.
 */
@Composable
internal fun LivingEyeMedallion(
    connected: Boolean,
    touchGaze: Offset? = null,
    opennessOverride: Float? = null,
    modifier: Modifier = Modifier,
) {

    val lids = ImageBitmap.imageResource(R.drawable.mobile_eye_reference_lids)
    val sclera = ImageBitmap.imageResource(R.drawable.mobile_eye_reference_sclera)
    val iris = ImageBitmap.imageResource(R.drawable.mobile_eye_reference_iris)
    val catchlight = ImageBitmap.imageResource(R.drawable.mobile_eye_reference_catchlight)
    val mesh = remember { ReferenceEyeMesh() }

    // 0 = reference opening; 1 = the same textured lids meeting at the contact seam.
    val lidPhase = remember { Animatable(1f) }
    val blinkEyeShift = remember { Animatable(0f) }
    val gazeX = remember { Animatable(0f) } // source-frame pixels
    val gazeY = remember { Animatable(0f) }
    val pupilScale = remember { Animatable(PUPIL_DARK_SCALE) }

    val blinkRandom = remember { Random(System.nanoTime().toInt()) }
    val gazeRandom = remember { Random(System.nanoTime().toInt() xor 0x4D414553) }
    val pupilRandom = remember { Random(System.nanoTime().toInt() xor 0x54524F56) }

    // Connection transitions and autonomous blinking share one lid clock, so they cannot race.
    LaunchedEffect(connected, opennessOverride) {
        blinkEyeShift.snapTo(0f)
        when {
            opennessOverride != null -> {
                lidPhase.snapTo(1f - opennessOverride.coerceIn(0f, 1f))
                return@LaunchedEffect
            }

            !connected -> {
                closeForDisconnect(lidPhase)
                return@LaunchedEffect
            }

            else -> openForConnection(lidPhase)
        }

        while (isActive) {
            delay(blinkRandom.nextBlinkDelayMillis())
            blinkOnce(lidPhase, blinkEyeShift)

            // Real double blinks occur, but should remain an occasional surprise.
            if (blinkRandom.nextFloat() < 0.10f) {
                delay(blinkRandom.nextLong(140L, 221L))
                blinkOnce(lidPhase, blinkEyeShift)
            }
        }
    }

    // A fixation is still; movement between fixations is a fast saccade, not smooth roaming.
    LaunchedEffect(connected, touchGaze, opennessOverride) {
        if (!connected || opennessOverride != null) {
            gazeTo(gazeX, gazeY, 0f, 0f, durationMillis = 45)
            return@LaunchedEffect
        }

        touchGaze?.let { target ->
            gazeTo(
                gazeX = gazeX,
                gazeY = gazeY,
                targetX = target.x.coerceIn(-1f, 1f) * MAX_GAZE_X,
                targetY = target.y.coerceIn(-1f, 1f) * MAX_GAZE_Y,
                durationMillis = 44,
            )
            return@LaunchedEffect
        }

        while (isActive) {
            delay(gazeRandom.nextLong(800L, 3_501L))

            val centreBias = if (gazeRandom.nextFloat() < 0.34f) 0.28f else 1f
            val targetX = gazeRandom.nextFloat(-MAX_GAZE_X, MAX_GAZE_X) * centreBias
            val targetY = gazeRandom.nextFloat(-MAX_GAZE_Y, MAX_GAZE_Y) * centreBias
            gazeTo(gazeX, gazeY, targetX, targetY, durationMillis = 42)

            // A rare sub-pixel microsaccade is visible at 60 Hz as one restrained step.
            if (gazeRandom.nextFloat() < 0.28f) {
                delay(gazeRandom.nextLong(180L, 521L))
                gazeTo(
                    gazeX,
                    gazeY,
                    (gazeX.value + gazeRandom.nextFloat(-0.7f, 0.7f))
                        .coerceIn(-MAX_GAZE_X, MAX_GAZE_X),
                    (gazeY.value + gazeRandom.nextFloat(-0.35f, 0.35f))
                        .coerceIn(-MAX_GAZE_Y, MAX_GAZE_Y),
                    durationMillis = 18,
                )
            }
        }
    }

    // Opening exposes the eye to light: latency, quick constriction, then slower redilation.
    // Idle "hippus" is irregular and small rather than a mechanical sine wave.
    LaunchedEffect(connected, opennessOverride) {
        if (!connected || opennessOverride != null) {
            pupilScale.snapTo(PUPIL_DARK_SCALE)
            return@LaunchedEffect
        }

        pupilScale.snapTo(PUPIL_DARK_SCALE)
        delay(230L)
        pupilScale.animateTo(
            PUPIL_BRIGHT_SCALE,
            tween(durationMillis = 420, easing = FastOutSlowInEasing),
        )
        pupilScale.animateTo(
            1f,
            tween(durationMillis = 980, easing = LinearOutSlowInEasing),
        )

        while (isActive) {
            delay(pupilRandom.nextLong(1_200L, 2_801L))
            pupilScale.animateTo(
                pupilRandom.nextFloat(0.975f, 1.026f),
                tween(
                    durationMillis = pupilRandom.nextInt(620, 1_101),
                    easing = FastOutSlowInEasing,
                ),
            )
        }
    }

    Canvas(modifier = modifier) {
        drawReferenceEye(
            lids = lids,
            sclera = sclera,
            iris = iris,
            catchlight = catchlight,
            mesh = mesh,
            closure = lidPhase.value,
            gazeX = gazeX.value - BLINK_NASAL_SHIFT * blinkEyeShift.value,
            gazeY = gazeY.value + BLINK_DOWN_SHIFT * blinkEyeShift.value,
            pupilScale = pupilScale.value,
        )
    }
}

private suspend fun openForConnection(lid: Animatable<Float, AnimationVector1D>) {
    if (lid.value > 0.5f) {
        lid.animateTo(
            0.5f,
            tween(durationMillis = 140, easing = LinearOutSlowInEasing),
        )
    }
    lid.animateTo(
        0f,
        tween(durationMillis = 290, easing = LinearOutSlowInEasing),
    )
}

private suspend fun closeForDisconnect(lid: Animatable<Float, AnimationVector1D>) {
    if (lid.value < 0.5f) {
        lid.animateTo(
            0.5f,
            tween(durationMillis = 90, easing = FastOutLinearInEasing),
        )
    }
    lid.animateTo(
        1f,
        tween(durationMillis = 160, easing = FastOutSlowInEasing),
    )
}

private suspend fun blinkOnce(
    lid: Animatable<Float, AnimationVector1D>,
    eyeShift: Animatable<Float, AnimationVector1D>,
) {
    coroutineScope {
        launch {
            lid.animateTo(
                0.5f,
                tween(durationMillis = 34, easing = FastOutLinearInEasing),
            )
            lid.animateTo(
                1f,
                tween(durationMillis = 58, easing = FastOutLinearInEasing),
            )
        }
        launch {
            eyeShift.animateTo(
                1f,
                tween(durationMillis = 92, easing = FastOutLinearInEasing),
            )
        }
    }
    delay(26L)
    coroutineScope {
        launch {
            lid.animateTo(
                0.5f,
                tween(durationMillis = 80, easing = LinearOutSlowInEasing),
            )
            lid.animateTo(
                0f,
                tween(durationMillis = 125, easing = LinearOutSlowInEasing),
            )
        }
        launch {
            eyeShift.animateTo(
                0f,
                tween(durationMillis = 205, easing = LinearOutSlowInEasing),
            )
        }
    }
}

private suspend fun gazeTo(
    gazeX: Animatable<Float, AnimationVector1D>,
    gazeY: Animatable<Float, AnimationVector1D>,
    targetX: Float,
    targetY: Float,
    durationMillis: Int,
) = coroutineScope {
    launch {
        gazeX.animateTo(
            targetX,
            tween(durationMillis = durationMillis, easing = FastOutSlowInEasing),
        )
    }
    launch {
        gazeY.animateTo(
            targetY,
            tween(durationMillis = durationMillis, easing = FastOutSlowInEasing),
        )
    }
}

private fun Random.nextBlinkDelayMillis(): Long {
    val u1 = nextDouble().coerceAtLeast(0.000_001)
    val u2 = nextDouble()
    val gaussian = sqrt(-2.0 * ln(u1)) * cos(2.0 * PI * u2)
    val seconds = exp(ln(4.2) + 0.45 * gaussian).coerceIn(1.5, 9.5)
    return (seconds * 1_000.0).roundToLong()
}

private fun Random.nextFloat(from: Float, until: Float): Float =
    nextDouble(from.toDouble(), until.toDouble()).toFloat()

private const val PUPIL_NEUTRAL_RADIUS = 54f
private const val PUPIL_BRIGHT_SCALE = 43f / PUPIL_NEUTRAL_RADIUS
private const val PUPIL_DARK_SCALE = 66f / PUPIL_NEUTRAL_RADIUS
private const val MAX_GAZE_X = 7f
private const val MAX_GAZE_Y = 4f
private const val BLINK_NASAL_SHIFT = 1.2f
private const val BLINK_DOWN_SHIFT = 2f

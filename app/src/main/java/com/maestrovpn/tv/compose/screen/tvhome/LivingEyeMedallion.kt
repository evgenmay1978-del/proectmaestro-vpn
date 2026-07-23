package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationVector1D
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Outline
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlin.random.Random

/**
 * Phone-only living eye layer for the home medallion.
 *
 * `mobile_eye_open` is a square, fully-open medallion render. Only its inner eye is ever
 * revealed through [AlmondApertureShape], so the outer ring baked into the fixed phone
 * background remains the sole visible ring and cannot jump during state changes.
 *
 * Disconnected is deliberately motionless and visually empty. Connected opens once, then
 * performs sparse blinks. Compose animations inherit the platform motion-duration scale, so
 * disabling animations in Android settings makes these transitions settle immediately.
 */
@Composable
internal fun LivingEyeMedallion(
    connected: Boolean,
    opennessOverride: Float? = null,
    modifier: Modifier = Modifier,
) {
    val opening = remember { Animatable(0f) }
    val blink = remember { Animatable(0f) }
    val random = remember { Random(System.nanoTime().toInt()) }

    LaunchedEffect(connected, opennessOverride) {
        when {
            opennessOverride != null -> opening.snapTo(opennessOverride.coerceIn(0f, 1f))
            connected -> {
                opening.snapTo(0f)
                opening.animateTo(
                    targetValue = 1f,
                    animationSpec = tween(
                        durationMillis = 620,
                        easing = FastOutSlowInEasing,
                    ),
                )
            }
            else -> opening.animateTo(
                targetValue = 0f,
                animationSpec = tween(
                    durationMillis = 360,
                    easing = FastOutSlowInEasing,
                ),
            )
        }
    }

    LaunchedEffect(connected, opennessOverride) {
        blink.snapTo(0f)
        if (!connected || opennessOverride != null) return@LaunchedEffect

        while (isActive) {
            delay(random.nextLong(from = 4_000L, until = 8_001L))
            blinkOnce(blink)

            // A double blink should be occasional, not a repeating mechanical pattern.
            if (random.nextFloat() < 0.18f) {
                delay(random.nextLong(from = 110L, until = 181L))
                blinkOnce(blink)
            }
        }
    }

    val apertureOpen = opennessOverride?.coerceIn(0f, 1f)
        ?: (opening.value * (1f - blink.value)).coerceIn(0f, 1f)
    val apertureShape = AlmondApertureShape(apertureOpen)

    Box(
        modifier = modifier.drawWithContent {
            drawContent()
            if (apertureOpen > 0.01f) {
                val aperture = almondPath(size, apertureOpen)
                val alpha = apertureOpen

                // Two restrained edge strokes give the open eye depth without a neon halo.
                drawPath(
                    path = aperture,
                    color = Color(0xFF2F9A63).copy(alpha = 0.055f * alpha),
                    style = Stroke(width = 4.dp.toPx()),
                )
                drawPath(
                    path = aperture,
                    color = Color(0xFF89C998).copy(alpha = 0.14f * alpha),
                    style = Stroke(width = 1.dp.toPx()),
                )
            }
        },
    ) {
        Image(
            painter = painterResource(R.drawable.mobile_eye_open),
            contentDescription = null,
            contentScale = ContentScale.Fit,
            modifier = Modifier
                .matchParentSize()
                .clip(apertureShape),
        )
    }
}

private suspend fun blinkOnce(blink: Animatable<Float, AnimationVector1D>) {
    blink.animateTo(
        targetValue = 1f,
        animationSpec = tween(
            durationMillis = 92,
            easing = FastOutSlowInEasing,
        ),
    )
    blink.animateTo(
        targetValue = 0f,
        animationSpec = tween(
            durationMillis = 155,
            easing = LinearOutSlowInEasing,
        ),
    )
}

/**
 * Fixed-width eyelid aperture. Opening changes only its height, which reads as two lids
 * separating naturally; the safe inset keeps every part of the baked outer ring hidden.
 */
private data class AlmondApertureShape(
    private val openness: Float,
) : Shape {
    override fun createOutline(
        size: Size,
        layoutDirection: LayoutDirection,
        density: Density,
    ): Outline = Outline.Generic(almondPath(size, openness))
}

private fun almondPath(size: Size, openness: Float): Path {
    val open = openness.coerceIn(0f, 1f)
    val centerX = size.width * 0.5f
    val centerY = size.height * 0.505f
    val halfWidth = size.width * 0.365f
    // Owner correction: use the full open-eye footprint, not a reduced inner insert.
    val upperHeight = size.height * 0.315f * open
    val lowerHeight = size.height * 0.295f * open
    val left = centerX - halfWidth
    val right = centerX + halfWidth
    val control = halfWidth * 0.58f

    return Path().apply {
        moveTo(left, centerY)
        cubicTo(
            centerX - control,
            centerY - upperHeight,
            centerX + control,
            centerY - upperHeight,
            right,
            centerY,
        )
        cubicTo(
            centerX + control,
            centerY + lowerHeight,
            centerX - control,
            centerY + lowerHeight,
            left,
            centerY,
        )
        close()
    }
}

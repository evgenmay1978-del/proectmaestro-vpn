package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import com.maestrovpn.tv.R

/**
 * Approval medallion of the phone home: the static "M" disc with an optional live emerald glow.
 *
 * Replaces the old living-eye medallion (2026-09-26, owner decision): the eye and all of its
 * animation sources are gone, the medallion itself never animates — only the two glow layers do.
 * The disc art is authored so that its visible circle fills the whole drawable, so the caller's
 * square box IS the medallion circle; both glow layers are scaled layers around that same circle
 * and therefore stay concentric with the disc by construction.
 */
private const val GlowClipScale = 1.10f

@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    val transition = rememberInfiniteTransition(label = "monogram")
    // Soft breathing of the halo: slow, wide, low amplitude.
    val haloAlpha by transition.animateFloat(
        initialValue = 0.25f,
        targetValue = 0.45f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloAlpha",
    )
    val haloScale by transition.animateFloat(
        initialValue = 0.99f,
        targetValue = 1.05f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloScale",
    )
    // Fast flicker of the rim: a living lamp, not a strobe — amplitude stays inside 0.75..1.0.
    val ringAlpha by transition.animateFloat(
        initialValue = 0.92f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = keyframes {
                durationMillis = 1200
                0.85f at 0
                1f at 90
                0.90f at 170
                1f at 260
                0.93f at 420
                1f at 700
                0.97f at 1200
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "ringFlicker",
    )

    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        Image(
            painter = painterResource(
                if (connected) R.drawable.mobile_medallion_m_on else R.drawable.mobile_medallion_m_off,
            ),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
    }
}

/**
 * The live glow of the medallion, drawn as its OWN layer ON TOP of the approved frame.
 *
 * Why separate: the frame is painted after the medallion and its ornament band covers everything
 * outside the opening, so a ring drawn under it is simply invisible (measured on the owner's phone
 * 26.09.2026). The glow is hard-clipped to a circle barely larger than the disc, so it can never
 * bleed over the ornament — exactly the placement the owner asked for.
 */
@Composable
internal fun MonogramGlow(connected: Boolean, modifier: Modifier = Modifier) {
    if (!connected) return
    val transition = rememberInfiniteTransition(label = "monogramGlow")
    val haloAlpha by transition.animateFloat(
        initialValue = 0.25f,
        targetValue = 0.45f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloAlpha",
    )
    val haloScale by transition.animateFloat(
        initialValue = 0.99f,
        targetValue = 1.05f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloScale",
    )
    val ringAlpha by transition.animateFloat(
        initialValue = 0.92f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = keyframes {
                durationMillis = 1200
                0.85f at 0
                1f at 90
                0.90f at 170
                1f at 260
                0.93f at 420
                1f at 700
                0.97f at 1200
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "ringFlicker",
    )
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        Box(
            modifier = Modifier
                .matchParentSize()
                .graphicsLayer { scaleX = GlowClipScale; scaleY = GlowClipScale }
                .clip(CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Image(
                painter = painterResource(R.drawable.mobile_medallion_glow_halo),
                contentDescription = null,
                modifier = Modifier.matchParentSize().graphicsLayer {
                    alpha = haloAlpha
                    scaleX = 1.18f * haloScale
                    scaleY = 1.18f * haloScale
                },
            )
            Image(
                painter = painterResource(R.drawable.mobile_medallion_glow_ring),
                contentDescription = null,
                modifier = Modifier.matchParentSize().graphicsLayer {
                    alpha = ringAlpha
                    scaleX = 1.08f
                    scaleY = 1.08f
                },
            )
        }
    }
}

@Composable
private fun UnusedLegacyGlow(connected: Boolean, modifier: Modifier = Modifier) {
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        if (connected) {
            // The glow lives strictly inside the frame's opening: it is hard-clipped to a circle
            // barely larger than the disc, so it can never bleed under the frame's ornament layer
            // (owner, 26.09.2026: «мерцание должно быть там, где я нарисовал, а не за орнаментом»).
            Box(
                modifier = Modifier
                    .matchParentSize()
                    .graphicsLayer { scaleX = GlowClipScale; scaleY = GlowClipScale }
                    .clip(CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Image(
                    painter = painterResource(R.drawable.mobile_medallion_glow_halo),
                    contentDescription = null,
                    modifier = Modifier.matchParentSize().graphicsLayer {
                        alpha = haloAlpha
                        scaleX = 1.18f * haloScale
                        scaleY = 1.18f * haloScale
                    },
                )
                Image(
                    painter = painterResource(R.drawable.mobile_medallion_glow_ring),
                    contentDescription = null,
                    modifier = Modifier.matchParentSize().graphicsLayer {
                        alpha = ringAlpha
                        scaleX = 1.08f
                        scaleY = 1.08f
                    },
                )
            }
        }
        Image(
            painter = painterResource(
                if (connected) R.drawable.mobile_medallion_m_on else R.drawable.mobile_medallion_m_off,
            ),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
    }
}

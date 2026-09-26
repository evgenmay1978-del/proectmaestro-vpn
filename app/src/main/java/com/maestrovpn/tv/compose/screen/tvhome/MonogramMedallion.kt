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
import androidx.compose.foundation.layout.matchParentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    val transition = rememberInfiniteTransition(label = "monogram")
    // Soft breathing of the halo: slow, wide, low amplitude.
    val haloAlpha by transition.animateFloat(
        initialValue = 0.40f,
        targetValue = 0.78f,
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
                0.75f at 0
                1f at 90
                0.82f at 170
                1f at 260
                0.88f at 420
                1f at 700
                0.95f at 1200
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "ringFlicker",
    )

    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        if (connected) {
            Image(
                painter = painterResource(R.drawable.mobile_medallion_glow_halo),
                contentDescription = null,
                modifier = Modifier.matchParentSize().graphicsLayer {
                    alpha = haloAlpha
                    scaleX = 1.70f * haloScale
                    scaleY = 1.70f * haloScale
                },
            )
            Image(
                painter = painterResource(R.drawable.mobile_medallion_glow_ring),
                contentDescription = null,
                modifier = Modifier.matchParentSize().graphicsLayer {
                    alpha = ringAlpha
                    scaleX = 1.02f
                    scaleY = 1.02f
                },
            )
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

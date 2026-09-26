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

/** Hard limit of the glow: barely outside the disc, so it can never reach the frame's ornament. */
private const val GlowClipScale = 1.10f

/**
 * The static "M" medallion with the soft halo that lives UNDER the disc.
 *
 * The halo must stay below the disc, otherwise it washes the leather grey around the letter
 * (owner's screen, 26.09.2026). Only the rim ring goes above the frame — see [MonogramGlow].
 */
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    val transition = rememberInfiniteTransition(label = "monogram")
    val haloAlpha by transition.animateFloat(
        initialValue = 0.20f,
        targetValue = 0.35f,
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

    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        if (connected) {
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
                        scaleX = 1.05f * haloScale
                        scaleY = 1.05f * haloScale
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

/**
 * The live rim of the medallion: drawn as its OWN layer ON TOP of the approved frame, because the
 * frame is painted after the medallion and its ornament band hides anything drawn outside the
 * opening (measured on the owner's phone 26.09.2026). Scale is exactly 1.0, i.e. the ring sits on
 * the leather edge — where the owner drew it — and never on the ornament.
 */
@Composable
internal fun MonogramGlow(connected: Boolean, modifier: Modifier = Modifier) {
    if (!connected) return
    val transition = rememberInfiniteTransition(label = "monogramGlow")
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
            painter = painterResource(R.drawable.mobile_medallion_glow_ring),
            contentDescription = null,
            modifier = Modifier.fillMaxSize().graphicsLayer { alpha = ringAlpha },
        )
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import com.maestrovpn.tv.R

/**
 * The static "M" medallion. No halo and no other white light: the owner removed it from the design
 * (26.09.2026) — the leather must stay dark, only the emerald rim lives and flickers ([MonogramGlow]).
 */
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
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
 * The live emerald rim of the medallion, drawn as its OWN layer ON TOP of the approved frame: the
 * frame is painted after the medallion and its ornament band hides anything drawn outside the
 * opening (measured on the owner's phone, 26.09.2026). Scale is exactly 1.0, so the ring sits on
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

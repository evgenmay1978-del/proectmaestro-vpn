package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * TV hero connect button — the Dark-Fantasy medallion, STATIC (owner: «тв версии без анимаций»,
 * 1 GB Sony/TCL boxes). Layers (back→front): green glow → the owner's chrome ring + green web
 * (home_medallion_bg) → the brand VM gem emblem in the centre → a static inner-rim glow → the
 * transparent connect Button (keeps D-pad focus + toggle). NO perpetual clocks / withFrameNanos:
 * the only motion is a one-shot 900 ms `power` cross-fade on connect/disconnect and the 140 ms
 * focus/press scale for the remote — both settle to 0 fps at idle, so the screen never repaints
 * while resting. Replaces the old rigged crawling spider (gait/scramble/RingShine clocks removed).
 */
@Composable
fun StaticTvMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier = Modifier,
) {
    // one-shot cross-fade: bright when connected, dim when off. Animates only during the
    // transition moment, then holds → no idle repaint.
    val power by animateFloatAsState(
        if (connected) 1f else 0.16f, tween(900, easing = FastOutSlowInEasing), label = "power",
    )

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val btnScale by animateFloatAsState(
        if (pressed) 0.94f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "medScale",
    )

    val bgP = painterResource(R.drawable.home_medallion_bg)
    val emblemP = painterResource(R.drawable.home_vm_emblem)
    // desaturate the chrome/web when powered down so it reads as "asleep", full colour when on.
    val webFilter = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0.15f + 0.85f * power) })

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.size(252.dp).graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // green glow hugging the medallion — brightens with power + focus (static, draw-phase)
        Box(
            Modifier.size(252.dp).drawBehind {
                val c = Offset(size.width / 2f, size.height / 2f)
                val r = size.minDimension * 0.5f
                val a = 0.16f + 0.30f * power + if (focused) 0.26f else 0f
                drawCircle(
                    brush = Brush.radialGradient(
                        0.00f to Color.Transparent, 0.64f to Color.Transparent,
                        0.82f to NeonGreen.copy(alpha = a), 1.00f to Color.Transparent,
                        center = c, radius = r * 1.16f,
                    ),
                    radius = r * 1.16f, center = c,
                )
            },
        )

        Box(Modifier.size(232.dp).clip(CircleShape)) {
            // 1) the owner's clean button — chrome ring + green web
            Image(bgP, null, Modifier.size(232.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)

            // 2) static green inner-rim glow (brightens with power)
            Box(
                Modifier.size(232.dp).drawBehind {
                    val c = Offset(size.width / 2f, size.height / 2f)
                    val r = size.minDimension / 2f
                    drawCircle(
                        brush = Brush.radialGradient(
                            0.62f to Color.Transparent,
                            0.78f to NeonGreen.copy(alpha = 0.05f + 0.22f * power),
                            0.88f to Color.Transparent,
                            center = c, radius = r,
                        ),
                        radius = r, center = c,
                    )
                },
            )

            // 3) brand VM gem emblem in the centre (the new icon), with a soft green underglow
            //    when connected so it reads as "alive/powered" without any motion.
            Box(Modifier.size(232.dp), contentAlignment = Alignment.Center) {
                Box(
                    Modifier.size(150.dp).drawBehind {
                        val g = 0.10f + 0.34f * power
                        drawCircle(
                            brush = Brush.radialGradient(
                                listOf(NeonGreen.copy(alpha = g), Color.Transparent),
                                center = Offset(size.width / 2f, size.height / 2f),
                                radius = size.minDimension * 0.5f,
                            ),
                        )
                    },
                )
                Image(
                    emblemP, null,
                    Modifier.size(132.dp).graphicsLayer { alpha = 0.55f + 0.45f * power },
                    contentScale = ContentScale.Fit,
                )
            }

            // 4) dim scrim when powered down
            Box(Modifier.size(232.dp).drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.28f)) })
        }

        Button(
            onClick = onToggle,
            shape = CircleShape,
            interactionSource = interaction,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier.size(200.dp).focusRequester(focusRequester),
            content = {},
        )
    }
}

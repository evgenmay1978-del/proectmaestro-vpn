package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
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
import kotlin.math.abs

/**
 * The hero connect button — the photoreal spider medallion from the owner's reference
 * (spiderinterfeis.png), rendered as THREE aligned layers so the spider can move with
 * physically-correct shadows and never skew:
 *
 *   1. [R.drawable.home_medallion_bg] — the steel ring + glowing green web, with the
 *      spider painted out (so no spider-shaped hole shows when it crawls away).
 *   2. [R.drawable.home_spider]       — the clean spider cut-out; the ONLY moving part.
 *   3. [R.drawable.home_ring]         — the chrome rim as a top layer, so the spider
 *      vanishes UNDER the rim as it crawls out instead of sliding over the frame.
 *
 * Motion is event-driven only (home stays static at rest, per the low-RAM TV budget):
 * on CONNECT the spider crawls up from under the bottom rim to centre and the web
 * powers up to full green; on DISCONNECT it crawls up and out the top while the web
 * desaturates ("powers down"). A soft contact shadow tracks the body by pure vertical
 * translation — it never rotates or shears, so there are no «перекосы».
 */
@Composable
fun SpiderMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier = Modifier,
) {
    // power 0..1: green web + glow when connected, greyed/dim when off.
    val power by animateFloatAsState(
        if (connected) 1f else 0.18f,
        tween(900, easing = FastOutSlowInEasing), label = "power",
    )
    // crawl position in fractions of the disk: 0 = centred, -1 = out the top,
    // +1 = below the bottom rim (hidden, the rest pose while disconnected).
    val pos = remember { Animatable(if (connected) 0f else 1f) }
    var first by remember { mutableStateOf(true) }
    LaunchedEffect(connected) {
        if (first) {
            first = false
            pos.snapTo(if (connected) 0f else 1f)
        } else if (connected) {
            pos.snapTo(1f)
            pos.animateTo(0f, tween(1300, easing = FastOutSlowInEasing))
        } else {
            pos.animateTo(-1f, tween(1100, easing = FastOutSlowInEasing))
            pos.snapTo(1f)
        }
    }

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val btnScale by animateFloatAsState(
        if (pressed) 0.94f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "medScale",
    )

    val bgP = painterResource(R.drawable.home_medallion_bg)
    val spiderP = painterResource(R.drawable.home_spider)
    val ringP = painterResource(R.drawable.home_ring)
    // Desaturate the web + notches toward grey as power drops ("lair powered off").
    val webFilter = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0.12f + 0.88f * power) })

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(252.dp)
            .graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // green glow ring hugging the medallion — brightens with power + focus
        Box(
            Modifier
                .size(252.dp)
                .drawBehind {
                    val c = Offset(size.width / 2f, size.height / 2f)
                    val r = size.minDimension * 0.5f
                    val a = 0.18f + 0.30f * power + if (focused) 0.26f else 0f
                    drawCircle(
                        brush = Brush.radialGradient(
                            0.00f to Color.Transparent,
                            0.64f to Color.Transparent,
                            0.82f to NeonGreen.copy(alpha = a),
                            1.00f to Color.Transparent,
                            center = c,
                            radius = r * 1.16f,
                        ),
                        radius = r * 1.16f,
                        center = c,
                    )
                },
        )

        // medallion disk (clipped) — bg, contact shadow, moving spider, rim overlay
        Box(Modifier.size(232.dp).clip(CircleShape)) {
            Image(
                bgP, contentDescription = null,
                modifier = Modifier.size(232.dp),
                contentScale = ContentScale.Fit, colorFilter = webFilter,
            )
            // contact shadow — a soft ellipse beneath the abdomen, tracking the body by
            // pure translation; fades out as the spider leaves the disk. No skew, ever.
            Box(
                Modifier
                    .size(232.dp)
                    .drawBehind {
                        val posY = pos.value * size.height * 0.80f
                        val cx = size.width / 2f
                        val sy = size.height * 0.5f + posY + size.height * 0.15f
                        val rx = size.width * 0.30f
                        val ry = size.height * 0.13f
                        val a = 0.42f * (1f - abs(pos.value).coerceIn(0f, 1f))
                        drawOval(
                            brush = Brush.radialGradient(
                                listOf(Color.Black.copy(alpha = a), Color.Transparent),
                                center = Offset(cx, sy), radius = rx,
                            ),
                            topLeft = Offset(cx - rx, sy - ry),
                            size = Size(rx * 2f, ry * 2f),
                        )
                    },
            )
            Image(
                spiderP, contentDescription = null,
                modifier = Modifier
                    .size(232.dp)
                    .graphicsLayer { translationY = pos.value * size.height * 0.80f },
                contentScale = ContentScale.Fit,
            )
            Image(
                ringP, contentDescription = null,
                modifier = Modifier.size(232.dp),
                contentScale = ContentScale.Fit, colorFilter = webFilter,
            )
            // dim scrim when powered down
            Box(
                Modifier
                    .size(232.dp)
                    .drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.34f)) },
            )
        }

        // transparent focus/click target on top (D-pad focusable + tappable)
        Button(
            onClick = onToggle,
            shape = CircleShape,
            interactionSource = interaction,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier
                .size(200.dp)
                .focusRequester(focusRequester),
            content = {},
        )
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
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
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.NeonGreen
import kotlinx.coroutines.launch
import kotlin.math.PI
import kotlin.math.roundToInt
import kotlin.math.sin

/** One rigged leg: its drawable + pivot (fraction of the medallion) + gait phase. */
private data class LegRig(val res: Int, val px: Float, val py: Float, val phase: Float)

// Pivots/phases derived offline (rig_spider.py) from the OWNER's clean spider asset:
// each leg rotates about its root (hidden under the body) in an alternating tetrapod gait.
private val SPIDER_LEGS = listOf(
    LegRig(R.drawable.spider_leg_00, 0.5824f, 0.3388f, 3.1416f),
    LegRig(R.drawable.spider_leg_01, 0.4194f, 0.3516f, 0.0000f),
    LegRig(R.drawable.spider_leg_02, 0.4542f, 0.3168f, 3.1416f),
    LegRig(R.drawable.spider_leg_03, 0.3901f, 0.3974f, 0.0000f),
    LegRig(R.drawable.spider_leg_04, 0.6117f, 0.3883f, 0.0000f),
    LegRig(R.drawable.spider_leg_05, 0.6136f, 0.4414f, 3.1416f),
    LegRig(R.drawable.spider_leg_06, 0.3901f, 0.4487f, 3.1416f),
    LegRig(R.drawable.spider_leg_07, 0.6484f, 0.5366f, 0.0000f),
    LegRig(R.drawable.spider_leg_08, 0.3516f, 0.5330f, 0.0000f),
)

private const val IDLE_DEG = 2.6f     // gentle leg sway while connected & resting (idle)
private const val BURST_DEG = 7.0f    // extra leg sway during the crawl "scuttle"

/**
 * The hero connect button — the photoreal spider medallion from the owner's reference,
 * but the spider is RIGGED: its body + legs (from the owner's clean spider asset) are
 * separate layers, so it genuinely crawls — each leg swings about its own root in gait.
 *
 * Layers (back→front): web/ring background (the owner's clean button — no spider baked in)
 * · moving leg SHADOWS · the lit legs · the body · the chrome rim · a dim scrim. Because
 * the background web is already clean, when the spider is tucked away the medallion shows
 * a clean web — no artifact. Leg shadows are the same legs drawn dark + offset, so the
 * shadows move WITH the legs by pure translation/rotation — never skewed.
 *
 * Behaviour: DISCONNECTED → the spider is hidden UP under the TOP rim (between button &
 * ring). CONNECT → web powers up green and it crawls DOWN to centre, legs scuttling (a
 * burst that settles into a gentle idle sway). RESTING (connected) → legs keep a subtle
 * idle sway in place, going nowhere. DISCONNECT → it crawls back UP and hides; web dims.
 */
@Composable
fun SpiderMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier = Modifier,
) {
    val power by animateFloatAsState(
        if (connected) 1f else 0.16f, tween(900, easing = FastOutSlowInEasing), label = "power",
    )
    // liveliness of the legs: 1 while connected (idle sway), 0 while off (still)
    val live by animateFloatAsState(
        if (connected) 1f else 0f, tween(1000, easing = FastOutSlowInEasing), label = "live",
    )
    // perpetual gait clock (the only continuous animation; one float, draw-phase only)
    val gait by rememberInfiniteTransition(label = "gait").animateFloat(
        0f, (2f * PI).toFloat(),
        infiniteRepeatable(tween(1500, easing = LinearEasing), RepeatMode.Restart), label = "gaitClock",
    )
    // crawl "scuttle" burst (extra leg sway) + crawl position
    val burst = remember { Animatable(0f) }
    // crawl position: 0 = centred (connected), 1 = hidden UP under the top rim (disconnected).
    val pos = remember { Animatable(if (connected) 0f else 1f) }
    var first by remember { mutableStateOf(true) }
    LaunchedEffect(connected) {
        val wasFirst = first
        first = false
        if (wasFirst) {
            pos.snapTo(if (connected) 0f else 1f)
        } else {
            // CONNECT → crawl DOWN from under the top rim to centre; DISCONNECT → crawl
            // back UP and hide. Both run as children of this effect, so a re-toggle
            // cancels the in-flight burst AND crawl cleanly (no animation fight).
            burst.snapTo(1f)
            launch { burst.animateTo(0f, tween(1700, easing = FastOutSlowInEasing)) }
            launch { pos.animateTo(if (connected) 0f else 1f, tween(1400, easing = FastOutSlowInEasing)) }
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
    val ringP = painterResource(R.drawable.home_ring)
    val bodyBmp = ImageBitmap.imageResource(R.drawable.spider_body)
    val legBmps = SPIDER_LEGS.map { ImageBitmap.imageResource(it.res) }
    val webFilter = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0.12f + 0.88f * power) })

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.size(252.dp).graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // green glow hugging the medallion — brightens with power + focus
        Box(
            Modifier.size(252.dp).drawBehind {
                val c = Offset(size.width / 2f, size.height / 2f)
                val r = size.minDimension * 0.5f
                val a = 0.18f + 0.30f * power + if (focused) 0.26f else 0f
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
            // 1) web + ring background (spider painted out)
            Image(bgP, null, Modifier.size(232.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)

            // 2) the rigged spider — drawn in ONE pass: leg shadows, legs, body
            Box(
                Modifier.size(232.dp).drawBehind {
                    val w = size.width
                    val h = size.height
                    val box = IntSize(w.roundToInt(), h.roundToInt())
                    val amp = IDLE_DEG * live + BURST_DEG * burst.value
                    val gy = -pos.value * h * 0.74f                   // hides UP under the top rim; crawls down to centre
                    val bob = (amp * 0.5f) * live * sin(gait)         // body bob, same gait freq as legs; still when off
                    val legShadow = ColorFilter.tint(Color.Black)

                    // leg SHADOWS (dark, offset down-right) — move with the legs
                    legBmps.forEachIndexed { i, bmp ->
                        val leg = SPIDER_LEGS[i]
                        val piv = Offset(leg.px * w, leg.py * h + gy)
                        val deg = amp * sin(gait + leg.phase)
                        withTransform({ rotate(deg, piv) }) {
                            drawImage(
                                bmp, dstOffset = IntOffset(5, (gy + 6f).roundToInt()), dstSize = box,
                                alpha = 0.28f, colorFilter = legShadow,
                            )
                        }
                    }
                    // lit legs
                    legBmps.forEachIndexed { i, bmp ->
                        val leg = SPIDER_LEGS[i]
                        val piv = Offset(leg.px * w, leg.py * h + gy)
                        val deg = amp * sin(gait + leg.phase)
                        withTransform({ rotate(deg, piv) }) {
                            drawImage(bmp, dstOffset = IntOffset(0, gy.roundToInt()), dstSize = box)
                        }
                    }
                    // body shadow + body (bob)
                    drawImage(
                        bodyBmp, dstOffset = IntOffset(5, (gy + bob + 6f).roundToInt()), dstSize = box,
                        alpha = 0.30f, colorFilter = legShadow,
                    )
                    drawImage(bodyBmp, dstOffset = IntOffset(0, (gy + bob).roundToInt()), dstSize = box)
                },
            )

            // 3) chrome rim on top, then dim scrim when powered down
            Image(ringP, null, Modifier.size(232.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)
            Box(Modifier.size(232.dp).drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.30f)) })
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

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
    LegRig(R.drawable.spider_leg_00, 0.5971f, 0.3150f, 3.1416f),
    LegRig(R.drawable.spider_leg_01, 0.4084f, 0.3278f, 0.0000f),
    LegRig(R.drawable.spider_leg_02, 0.4487f, 0.2875f, 3.1416f),
    LegRig(R.drawable.spider_leg_03, 0.3755f, 0.3810f, 0.0000f),
    LegRig(R.drawable.spider_leg_04, 0.6117f, 0.3901f, 0.0000f),
    LegRig(R.drawable.spider_leg_05, 0.6319f, 0.4322f, 3.1416f),
    LegRig(R.drawable.spider_leg_06, 0.3755f, 0.4396f, 3.1416f),
    LegRig(R.drawable.spider_leg_07, 0.3333f, 0.5366f, 0.0000f),
    LegRig(R.drawable.spider_leg_08, 0.6722f, 0.5421f, 0.0000f),
)

private const val IDLE_DEG = 2.9f     // gentle leg sway while connected & resting (idle)
private const val BURST_DEG = 17.0f   // big leg STRIDE/lift during the crawl, so it grips & climbs high

/**
 * The hero connect button — the photoreal spider medallion from the owner's reference,
 * but the spider is RIGGED: its body + legs (from the owner's clean spider asset) are
 * separate layers, so it genuinely crawls — each leg swings about its own root in gait.
 *
 * Layers (back→front): the owner's clean button (chrome ring + green web) · moving leg
 * SHADOWS · the lit legs · the body · a dim scrim. The spider is drawn OVER the ring (no
 * overlay) so legs that overflow the web rest ON the ring, and it's clipped to the medallion
 * circle so it vanishes at the rim. The clean button web means a tucked-away spider leaves
 * no artifact. Leg shadows are the same legs drawn dark + offset, tracking by pure
 * translation/rotation — never skewed.
 *
 * Behaviour (pass-through, like a real spider): DISCONNECTED → hidden under the BOTTOM rim.
 * CONNECT → web powers up green and it clambers UP from the bottom to centre, legs STEPPING
 * (a fast scramble that settles into a gentle slow idle sway in place). DISCONNECT → it keeps
 * clambering UP and off the TOP, then resets to the bottom for next time; web dims.
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
    // slow gait clock — the gentle idle leg sway (one float, draw-phase only)
    val gait by rememberInfiniteTransition(label = "gait").animateFloat(
        0f, (2f * PI).toFloat(),
        infiniteRepeatable(tween(2800, easing = LinearEasing), RepeatMode.Restart), label = "gaitClock",
    )
    // faster "scramble" clock — used (scaled by burst) ONLY while crawling, so the legs
    // visibly STEP as the body climbs (a real crawl, never a slide); idle stays slow.
    val scramble by rememberInfiniteTransition(label = "scr").animateFloat(
        0f, (2f * PI).toFloat(),
        infiniteRepeatable(tween(520, easing = LinearEasing), RepeatMode.Restart), label = "scrClock",
    )
    // crawl "scuttle" burst (extra leg sway) + crawl position
    val burst = remember { Animatable(0f) }
    // crawl position: 0 = centred, +1 = hidden below the bottom rim, -1 = climbed off the top.
    val pos = remember { Animatable(if (connected) 0f else 1f) }
    var first by remember { mutableStateOf(true) }
    LaunchedEffect(connected) {
        val wasFirst = first
        first = false
        if (wasFirst) {
            pos.snapTo(if (connected) 0f else 1f)
        } else {
            // CONNECT → clamber UP from the BOTTOM rim to centre in distinct STRIDES (a quick
            // pull, then a brief plant, ×4) so it grips and climbs like a real spider instead of
            // gliding. DISCONNECT → keep clambering UP and off the TOP, then reset to the bottom.
            // burst (the big leg sweep) is a cancellable child; pos animates here so a re-toggle
            // cancels both. The leg "scramble" clock keeps the legs stepping through each stride.
            burst.snapTo(1f)
            launch { burst.animateTo(0f, tween(5800, easing = LinearEasing)) }
            if (connected) {
                // ALWAYS emerge from the BOTTOM legs-first. If a disconnect was still mid-flight
                // (spider climbing up top, pos < 0), snap it back under the bottom rim first so a
                // fast off→on never makes it descend rear-first from the top.
                pos.snapTo(1f)
                // SMOOTH slow climb — FastOutSlowIn means a slow start (legs emerge first) and a
                // steady glide to centre, with NO keyframe holds → no jerks. The busy, big-sweep
                // legs carry the "walk", so it reads as crawling, not sliding.
                pos.animateTo(0f, tween(5800, easing = FastOutSlowInEasing))
            } else {
                pos.animateTo(-1f, tween(4200, easing = FastOutSlowInEasing))
                pos.snapTo(1f)
            }
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
            // 1) the owner's clean button (chrome ring + green web)
            Image(bgP, null, Modifier.size(232.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)

            // 2) the rigged spider — drawn in ONE pass: leg shadows, legs, body
            Box(
                Modifier.size(232.dp).drawBehind {
                    val w = size.width
                    val h = size.height
                    val box = IntSize(w.roundToInt(), h.roundToInt())
                    val idleA = IDLE_DEG * live
                    val crawlA = BURST_DEG * burst.value
                    // each leg = slow idle sway + a FAST scramble while crawling, so it steps
                    // (never slides) as it climbs, then settles to a gentle idle.
                    fun legDeg(phase: Float) = idleA * sin(gait + phase) + crawlA * sin(scramble + phase)
                    val gy = pos.value * h * 0.98f                    // +1 hides below, -1 climbs off the top
                    val bob = 0.5f * (idleA * sin(2f * gait) + crawlA * sin(2f * scramble))
                    val legShadow = ColorFilter.tint(Color.Black)

                    // leg SHADOWS (dark, offset) cast on the web/ring — track the legs exactly
                    legBmps.forEachIndexed { i, bmp ->
                        val leg = SPIDER_LEGS[i]
                        val piv = Offset(leg.px * w, leg.py * h + gy)
                        withTransform({ rotate(legDeg(leg.phase), piv) }) {
                            drawImage(
                                bmp, dstOffset = IntOffset(6, (gy + 8f).roundToInt()), dstSize = box,
                                alpha = 0.30f, colorFilter = legShadow,
                            )
                        }
                    }
                    // lit legs — drawn OVER the chrome ring, so legs that overflow the web rest on it
                    legBmps.forEachIndexed { i, bmp ->
                        val leg = SPIDER_LEGS[i]
                        val piv = Offset(leg.px * w, leg.py * h + gy)
                        withTransform({ rotate(legDeg(leg.phase), piv) }) {
                            drawImage(bmp, dstOffset = IntOffset(0, gy.roundToInt()), dstSize = box)
                        }
                    }
                    // body contact shadow + body (small bob)
                    drawImage(
                        bodyBmp, dstOffset = IntOffset(6, (gy + bob + 8f).roundToInt()), dstSize = box,
                        alpha = 0.34f, colorFilter = legShadow,
                    )
                    drawImage(bodyBmp, dstOffset = IntOffset(0, (gy + bob).roundToInt()), dstSize = box)
                },
            )

            // 3) dim scrim when powered down (the spider is drawn over the ring — no overlay)
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

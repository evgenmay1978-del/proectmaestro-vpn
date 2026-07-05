package com.maestrovpn.tv.compose.fantasy

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.util.lerp
import com.maestrovpn.tv.compose.theme.GoldDark
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.GoldLow
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.NeonGreenBright

/**
 * Dark-Fantasy switch — replaces the flat Material3 [androidx.compose.material3.Switch] on phone.
 * A dark-wood track ringed in aged bronze; the knob is a bronze bead at rest that slides to a
 * glowing EMERALD bead when ON (a soft green halo blooms behind it — the app's "active" language).
 *
 * Signature-compatible with Switch: pass `checked` + `onCheckedChange` (null = display-only).
 */
@Composable
fun FantasyToggle(
    checked: Boolean,
    onCheckedChange: ((Boolean) -> Unit)?,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    val t by animateFloatAsState(if (checked) 1f else 0f, tween(190), label = "toggle")
    val a = if (enabled) 1f else 0.4f
    Box(
        modifier
            .size(width = 52.dp, height = 30.dp)
            .then(
                if (onCheckedChange != null && enabled) {
                    Modifier.clickable(
                        interactionSource = rememberIS(),
                        indication = null,
                    ) { onCheckedChange(!checked) }
                } else {
                    Modifier
                },
            )
            .drawBehind {
                val r = size.height / 2f
                val cr = CornerRadius(r, r)
                // track fill — dark wood, warming faintly green when ON
                drawRoundRect(
                    brush = Brush.verticalGradient(
                        listOf(
                            lerpC(Color(0xFF1C130A), Color(0xFF10301C), t),
                            lerpC(Color(0xFF0B0805), Color(0xFF071C10), t),
                        ),
                    ),
                    cornerRadius = cr,
                    alpha = a,
                )
                // emerald inner glow when ON
                if (t > 0f) {
                    drawRoundRect(
                        color = NeonGreen.copy(alpha = 0.30f * t * a),
                        cornerRadius = cr,
                    )
                }
                // aged-bronze rim
                drawRoundRect(
                    brush = Brush.verticalGradient(listOf(GoldHi, GoldMid, GoldDark)),
                    cornerRadius = cr,
                    style = Stroke(width = 2.dp.toPx()),
                    alpha = a,
                )
                // knob — bronze bead → emerald bead
                val cx = lerp(r, size.width - r, t)
                val knobR = r - 4.dp.toPx()
                if (t > 0f) {
                    drawCircle(
                        color = NeonGreen.copy(alpha = 0.45f * t * a),
                        radius = knobR + 6.dp.toPx(),
                        center = Offset(cx, r),
                    )
                }
                drawCircle(
                    brush = Brush.verticalGradient(
                        listOf(
                            lerpC(GoldHi, NeonGreenBright, t),
                            lerpC(GoldLow, NeonGreen, t),
                        ),
                    ),
                    radius = knobR,
                    center = Offset(cx, r),
                    alpha = a,
                )
            },
    )
}

/**
 * Dark-Fantasy segmented toggle (2+ options) — e.g. the Android / iPhone switch in the share
 * dialog. A bronze-framed dark-wood pill; the selected segment lights up with an emerald-glass
 * fill + gold rim, unselected segments stay dim bronze.
 */
@Composable
fun FantasySegmented(
    options: List<String>,
    selected: Int,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(14.dp)
    Row(
        modifier
            .clip(shape)
            .background(Brush.verticalGradient(listOf(Color(0xFF1A120A), Color(0xFF0B0805))))
            .border(1.5.dp, Brush.verticalGradient(listOf(GoldHi, GoldMid, GoldDark)), shape)
            .padding(4.dp),
    ) {
        options.forEachIndexed { i, label ->
            val sel = i == selected
            val segShape = RoundedCornerShape(10.dp)
            Box(
                Modifier
                    .weight(1f)
                    .clip(segShape)
                    .then(
                        if (sel) {
                            Modifier.drawBehind {
                                val cr = CornerRadius(10.dp.toPx(), 10.dp.toPx())
                                drawRoundRect(NeonGreen.copy(alpha = 0.16f), cornerRadius = cr)
                                drawRoundRect(
                                    brush = Brush.verticalGradient(listOf(GoldHi, GoldLow)),
                                    cornerRadius = cr,
                                    style = Stroke(1.5.dp.toPx()),
                                )
                            }
                        } else {
                            Modifier
                        },
                    )
                    .clickable(interactionSource = rememberIS(), indication = null) { onSelect(i) }
                    .padding(vertical = 10.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    label,
                    color = if (sel) Color.White else GoldMid.copy(alpha = 0.75f),
                    fontWeight = if (sel) FontWeight.Bold else FontWeight.Medium,
                    textAlign = TextAlign.Center,
                )
            }
        }
    }
}

/** Local, allocation-light interaction source (no ripple — we draw our own affordance). */
@Composable
private fun rememberIS(): MutableInteractionSource =
    androidx.compose.runtime.remember { MutableInteractionSource() }

/** Channel-wise Color lerp (avoids importing the graphics overload name-clash with util.lerp). */
private fun lerpC(a: Color, b: Color, t: Float): Color = Color(
    red = lerp(a.red, b.red, t),
    green = lerp(a.green, b.green, t),
    blue = lerp(a.blue, b.blue, t),
    alpha = lerp(a.alpha, b.alpha, t),
)

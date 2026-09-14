package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R

/** Clean carved phone artwork; only the inner light moves during connection. */
@Composable
internal fun PhoneConnectionRing(connected: Boolean, connecting: Boolean, modifier: Modifier = Modifier) {
    val artwork = ImageBitmap.imageResource(R.drawable.phone_connection_medallion)
    val rotation = if (connecting) {
        val transition = rememberInfiniteTransition(label = "phone-connecting")
        val angle by transition.animateFloat(0f, 360f,
            infiniteRepeatable(tween(1_800, easing = LinearEasing), RepeatMode.Restart), label = "gold-segment")
        angle
    } else 0f
    val active = connected && !connecting
    Canvas(modifier) {
        if (size.minDimension <= 0f) return@Canvas
        // The original source stays intact. Alpha is applied only to the outer decoration;
        // its dark rectangular backdrop becomes transparent, while the real walnut disk is retained.
        val centre = Offset(size.width * 0.499601f, size.height * 0.446842f)
        val diskRadius = size.width * 0.269f
        val disk = Path().apply { addOval(Rect(centre - Offset(diskRadius, diskRadius), Size(diskRadius * 2, diskRadius * 2))) }
        val destination = IntSize(size.width.toInt(), size.height.toInt())
        clipPath(disk) { drawImage(artwork, dstSize = destination) }
        drawImage(artwork, dstSize = destination, colorFilter = ColorFilter.colorMatrix(ColorMatrix(floatArrayOf(
            1f, 0f, 0f, 0f, 0f,
            0f, 1f, 0f, 0f, 0f,
            0f, 0f, 1f, 0f, 0f,
            1.4f, 2.6f, 0f, 0f, -190f,
        ))))

        val radius = size.width * 0.258f
        val topLeft = centre - Offset(radius, radius)
        val arcSize = Size(radius * 2, radius * 2)
        if (active) {
            drawCircle(ConsoleEmerald.copy(alpha = .10f), radius, centre, style = Stroke(7.dp.toPx()))
            drawCircle(ConsoleEmerald.copy(alpha = .24f), radius, centre, style = Stroke(3.dp.toPx()))
        }
        drawCircle(if (active) ConsoleEmerald else ConsoleGold.copy(alpha = .3f),
            radius, centre, style = Stroke(1.4.dp.toPx()))
        if (connecting) {
            drawArc(ConsoleGold.copy(alpha = 0.4f), rotation - 140f, 90f, false,
                topLeft, arcSize, style = Stroke(1.7.dp.toPx(), cap = StrokeCap.Round))
            drawArc(Color(0xFFFFE2A4), rotation - 92f, 42f, false,
                topLeft, arcSize, style = Stroke(1.7.dp.toPx(), cap = StrokeCap.Round))
        }

        val powerRadius = radius * 0.24f
        // Balance the shaft's top and the arc's bottom around the measured disk centre.
        val powerCentre = centre + Offset(0f, powerRadius * 0.07f)
        val powerStroke = radius * 0.046f
        fun power(brush: Brush, width: Float, offset: Offset = Offset.Zero) {
            val c = powerCentre + offset
            drawArc(brush, -45f, 270f, false, c - Offset(powerRadius, powerRadius),
                Size(powerRadius * 2, powerRadius * 2), style = Stroke(width, cap = StrokeCap.Round))
            drawLine(brush, c - Offset(0f, powerRadius * 1.14f), c - Offset(0f, powerRadius * 0.05f),
                strokeWidth = width, cap = StrokeCap.Round)
        }
        power(Brush.verticalGradient(listOf(Color(0xFF100A04), Color(0xAA100A04))),
            powerStroke + 1.4.dp.toPx(), Offset(0.7.dp.toPx(), 1.1.dp.toPx()))
        power(Brush.linearGradient(if (active) listOf(Color(0xFF7EEAB6), ConsoleEmerald, Color(0xFF168E52))
            else listOf(ConsoleGoldLight, ConsoleGold, Color(0xFF9D7738)),
            centre - Offset(powerRadius, powerRadius), centre + Offset(powerRadius, powerRadius)), powerStroke)
        power(Brush.verticalGradient(listOf(Color(0xB3FFF1C6), Color(0x335E360F)),
            centre.y - powerRadius, centre.y + powerRadius), powerStroke * 0.24f,
            Offset(-0.2.dp.toPx(), -0.35.dp.toPx()))
    }
}

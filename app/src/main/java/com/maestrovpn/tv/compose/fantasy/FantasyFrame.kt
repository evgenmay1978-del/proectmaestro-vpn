package com.maestrovpn.tv.compose.fantasy

import android.graphics.LightingColorFilter
import androidx.annotation.DrawableRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import kotlin.math.roundToInt

/**
 * Dark-Fantasy frame primitive — the heart of the redesign's "one hand-crafted UI kit".
 *
 * Draws one of the aged-bronze + green-ivy frames SLICED FROM THE OWNER'S REFERENCE PIXELS
 * (`interplitki.png` → `res/drawable-nodpi/frame_*.9.png`) as a resolution-independent
 * NinePatch behind the content. The ornate corners (with ivy) stay fixed; the straight
 * bronze/wood edges + wood interior stretch — so ONE asset frames a tile, a wide panel or a
 * button at any size, pixel-faithful to the эскиз and crisp on any screen/DPI.
 *
 * This replaces the old flat 4-stop gradient border (`chromeBezelBrush`/`goldBezelBrush`) that
 * never matched the hand-painted kit. It is PHONE-only: TV keeps its chrome/glass language.
 *
 * @param selected warms the bronze toward amber (the active/selected tile in the reference).
 */
fun Modifier.fantasyFrame(
    @DrawableRes frameRes: Int,
    selected: Boolean = false,
): Modifier = composed {
    val ctx = LocalContext.current
    // The NinePatchDrawable is immutable art; remember it per (res, selected) so the warm
    // ColorFilter for the selected state is applied once, not every frame.
    val drawable = remember(frameRes, selected) {
        ContextCompat.getDrawable(ctx, frameRes)?.mutate()?.apply {
            colorFilter = if (selected) {
                // multiply the bronze toward warm amber (selected "Авто" in the эскиз)
                LightingColorFilter(SELECTED_WARM.toArgb(), 0x120A00)
            } else {
                null
            }
        }
    }
    drawBehind {
        val d = drawable ?: return@drawBehind
        d.setBounds(0, 0, size.width.roundToInt(), size.height.roundToInt())
        drawIntoCanvas { canvas -> d.draw(canvas.nativeCanvas) }
    }
}

/** Warm multiply applied to a selected bronze frame → amber-lit bezel (matches the эскиз). */
private val SELECTED_WARM = Color(0xFFFFC080)

/**
 * Чёткая ТВ-фокус-обводка для fantasy-поверхностей (поле ввода, строки настроек):
 * тонкий акцентный контур внутри бронзовой рамки, БЕЗ blur-глоу и state-layer-вуали
 * (цветные elevation-тени читались на ТВ как «зелёный засвет», фото owner 2026-07-11).
 * [alpha] — лямбда, чтобы анимация жила только в draw-фазе.
 */
internal fun Modifier.fantasyFocusFrame(alpha: () -> Float, color: Color, cornerRadius: Dp = 14.dp): Modifier =
    drawWithContent {
        drawContent()
        val a = alpha()
        if (a > 0.01f) {
            val inset = 4.dp.toPx()
            val rad = cornerRadius.toPx()
            drawRoundRect(
                color = color.copy(alpha = a),
                topLeft = Offset(inset, inset),
                size = Size(size.width - 2 * inset, size.height - 2 * inset),
                cornerRadius = CornerRadius(rad, rad),
                style = Stroke(width = 2.dp.toPx()),
            )
        }
    }

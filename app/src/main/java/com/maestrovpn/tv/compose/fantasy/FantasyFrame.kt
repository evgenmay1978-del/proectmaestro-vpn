package com.maestrovpn.tv.compose.fantasy

import android.graphics.LightingColorFilter
import androidx.annotation.DrawableRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
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

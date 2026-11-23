package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import kotlin.math.roundToInt

/** Crown used by the approved subscription and renewal controls. */
val MaestroCrown: ImageVector = ImageVector.Builder("MaestroCrown", 24.dp, 24.dp, 24f, 24f).apply {
    path(fill = SolidColor(Color.White)) {
        moveTo(1f, 6f); lineTo(6.5f, 10f); lineTo(12f, 2f); lineTo(17.5f, 10f)
        lineTo(23f, 6f); lineTo(20f, 18f); lineTo(4f, 18f); close()
        moveTo(4f, 20f); lineTo(20f, 20f); lineTo(20f, 22f); lineTo(4f, 22f); close()
    }
}.build()

/** Unmodified approved artwork. Only ornament/wood regions are sampled; UI text stays live. */
private data class ArtRect(val x: Int, val y: Int, val w: Int, val h: Int)

private fun DrawScope.art(image: ImageBitmap, source: ArtRect, x: Float, y: Float, w: Float, h: Float) {
    if (w <= 0f || h <= 0f) return
    val left = x.roundToInt()
    val top = y.roundToInt()
    drawImage(image, IntOffset(source.x, source.y), IntSize(source.w, source.h),
        IntOffset(left, top), IntSize((x + w).roundToInt().minus(left).coerceAtLeast(1),
            (y + h).roundToInt().minus(top).coerceAtLeast(1)))
}

/**
 * Панель утверждённого тёмного дизайна (23.11.2026): плоская тёмная карточка с тонкой
 * золото-оттенённой обводкой вместо резной nine-patch пластины. Выбранная — светлее
 * и с яркой золотой обводкой; навигация — полупрозрачная, с большим радиусом.
 * Одно место покрывает все телефонные панели/кнопки/навигацию (и fantasyFrame на
 * телефоне, который делегирует сюда же); на ТВ этот модификатор не используется.
 */
fun Modifier.approvedMobilePanel(selected: Boolean = false, navigation: Boolean = false): Modifier = composed {
    val bg = when {
        navigation -> DarkPanel.copy(alpha = 0.92f)
        selected -> DarkPanel2
        else -> DarkPanel
    }
    val border = when {
        navigation -> DarkLineSoft
        selected -> Color(0x8CD9A441) // rgba(217,164,65,.55)
        else -> DarkLineSoft
    }
    val radius = if (navigation) DarkNavCorner else DarkPanelCorner
    this
        .background(bg, RoundedCornerShape(radius))
        .border(1.dp, border, RoundedCornerShape(radius))
}

@Composable
fun ApprovedMobileBrand(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_carved_brand_reference)
    Canvas(modifier) {
        val canvas = drawContext.canvas
        canvas.saveLayer(Rect(Offset.Zero, size), Paint())
        art(atlas, ArtRect(65, 104, 724, 145), 0f, 0f, size.width, size.height)
        // The plaque is sampled from a mockup, so its rectangular photo edge used to cut across the
        // vine behind it. Fade the last few dp into the page wood instead of showing a hard border.
        val edge = 7.dp.toPx()
        val xEdge = (edge / size.width).coerceAtMost(0.15f)
        val yEdge = (edge / size.height).coerceAtMost(0.25f)
        drawRect(Brush.horizontalGradient(0f to Color.Transparent, xEdge to Color.Black,
            (1f - xEdge) to Color.Black, 1f to Color.Transparent), blendMode = BlendMode.DstIn)
        drawRect(Brush.verticalGradient(0f to Color.Transparent, yEdge to Color.Black,
            (1f - yEdge) to Color.Black, 1f to Color.Transparent), blendMode = BlendMode.DstIn)
        canvas.restore()
    }
}

// Registration against the owner's approved carved reference, in source pixels.
internal const val CARVED_MEDALLION_ASPECT = 720f / 712f
internal const val CARVED_EYE_LEFT = 148f / 712f
internal const val CARVED_EYE_TOP = 81f / 720f
internal const val CARVED_EYE_DIAMETER = 418f / 712f

// Monogram medallion (26.09.2026): the owner-approved "M" disc replaces the living eye. The disc
// fills the frame's inner opening (measured from the owner's mockup: ~0.80 of the frame box) and
// stays concentric with the eye's centre, so the approved frame art still lines up exactly.
internal const val CARVED_MEDALLION_DIAMETER = 570f / 712f
internal const val CARVED_MEDALLION_LEFT = 72f / 712f
internal const val CARVED_MEDALLION_TOP = 5f / 720f

@Composable
fun ApprovedMobileBackground(modifier: Modifier = Modifier) {
    // Утверждённый тёмный дизайн (23.11.2026): чистый тёмный фон #0d0b08 с лёгким
    // золотым свечением сверху и снизу (как в прототипе). Дерево (phone_home_wood)
    // и резные рейки/капитель из атласа здесь больше не рисуются.
    Box(modifier.background(DarkBg)) {
        Canvas(Modifier.fillMaxSize()) {
            drawRect(
                brush = Brush.radialGradient(
                    0f to Color(0x29D9A441), // rgba(217,164,65,.16)
                    1f to Color.Transparent,
                    center = Offset(size.width / 2f, -size.height * 0.06f),
                    radius = size.width * 0.9f,
                ),
            )
            drawRect(
                brush = Brush.radialGradient(
                    0f to Color(0x12D9A441), // rgba(217,164,65,.07)
                    1f to Color.Transparent,
                    center = Offset(size.width / 2f, size.height * 1.08f),
                    radius = size.width * 0.75f,
                ),
            )
        }
    }
}

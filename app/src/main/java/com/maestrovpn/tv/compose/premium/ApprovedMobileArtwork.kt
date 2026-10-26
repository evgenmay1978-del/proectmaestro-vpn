package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.draw.drawBehind
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
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
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
 * Plates are clean nine-patch assets (built by tools/build-phone-plates.py from the approved atlas):
 * ornament, rails and shadows are byte-identical to the mockup, while the baked icon/text/chevron is
 * replaced by plain grain. Android therefore stretches only the flat interior — no grain smear, no
 * brightness step at the corner seam and no leftover letters, which the previous hand-made 9-slice
 * could not avoid.
 */
private fun panelResource(selected: Boolean, navigation: Boolean): Int = when {
    navigation -> R.drawable.mobile_phone_bar
    selected -> R.drawable.mobile_phone_selected
    else -> R.drawable.mobile_phone_panel
}

fun Modifier.approvedMobilePanel(selected: Boolean = false, navigation: Boolean = false): Modifier = composed {
    val context = LocalContext.current
    val plate = remember(selected, navigation) { ContextCompat.getDrawable(context, panelResource(selected, navigation)) }
    drawBehind {
        val drawable = plate ?: return@drawBehind
        drawable.setBounds(0, 0, size.width.roundToInt(), size.height.roundToInt())
        drawIntoCanvas { canvas -> drawable.draw(canvas.nativeCanvas) }
    }
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
fun ApprovedMobileEyeFrame(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_carved_medallion_reference)
    Canvas(modifier) {
        val scale = size.width / 712f
        val centre = Offset(357f * scale, 290f * scale)
        val radius = 207f * scale
        val silhouette = Path().apply {
            fillType = PathFillType.EvenOdd
            addRect(Rect(Offset.Zero, size))
            addOval(Rect(centre.x - radius, centre.y - radius, centre.x + radius, centre.y + radius))
        }
        val canvas = drawContext.canvas
        canvas.saveLayer(Rect(Offset.Zero, size), Paint())
        // The source image is unchanged; the live eye occupies a clipped opening in the UI.
        clipPath(silhouette) { art(atlas, ArtRect(70, 536, 712, 720), 0f, 0f, size.width, size.height) }
        // Blend the sampled carving into the surrounding wood without a rectangular photo edge.
        val edge = 5.dp.toPx()
        val xEdge = (edge / size.width).coerceAtMost(0.1f)
        val yEdge = (edge / size.height).coerceAtMost(0.1f)
        drawRect(Brush.horizontalGradient(0f to Color.Transparent, xEdge to Color.Black,
            (1f - xEdge) to Color.Black, 1f to Color.Transparent), blendMode = BlendMode.DstIn)
        drawRect(Brush.verticalGradient(0f to Color.Transparent, yEdge to Color.Black,
            (1f - yEdge) to Color.Black, 1f to Color.Transparent), blendMode = BlendMode.DstIn)
        canvas.restore()
    }
}

@Composable
fun ApprovedMobileBackground(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    Box(modifier) {
        Image(painterResource(R.drawable.phone_home_wood), null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        Canvas(Modifier.fillMaxSize()) {
            val side = (size.width * 72f / 852f).coerceAtMost(42.dp.toPx())
            art(atlas, ArtRect(0, 0, 72, 1628), 0f, 0f, side, size.height)
            art(atlas, ArtRect(780, 0, 72, 1628), size.width - side, 0f, side, size.height)
            val capSize = Size(size.width - side * 2, (size.width - side * 2) * 82f / 708f)
            val capOrigin = Offset(side, 0f)
            val canvas = drawContext.canvas
            canvas.saveLayer(Rect(capOrigin, capSize), Paint())
            art(atlas, ArtRect(72, 0, 708, 82), side, 0f, capSize.width, capSize.height)
            drawRect(
                brush = Brush.verticalGradient(0f to Color.Black, 0.85f to Color.Black,
                    1f to Color.Transparent, startY = 0f, endY = capSize.height),
                topLeft = capOrigin,
                size = capSize,
                blendMode = BlendMode.DstIn,
            )
            canvas.restore()
        }
    }
}

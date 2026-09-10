package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
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
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
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

private fun DrawScope.panel(image: ImageBitmap, selected: Boolean, navigation: Boolean) {
    val source = when {
        navigation -> ArtRect(13, 1632, 826, 186)
        selected -> ArtRect(87, 1174, 374, 87)
        else -> ArtRect(72, 252, 708, 126)
    }
    val corner = if (selected && !navigation) 20 else 32
    val edge = minOf(if (navigation) 16.dp.toPx() else 13.dp.toPx(), size.width / 3, size.height / 3)
    // Each clean column spans the same rows as its frame, so the grain joins at the slice edges.
    val wood = when {
        navigation -> ArtRect(246, source.y, 24, source.h)
        selected -> ArtRect(182, source.y, 20, source.h)
        else -> ArtRect(110, source.y, 118, source.h)
    }
    val sx = intArrayOf(source.x, source.x + corner, source.x + source.w - corner)
    val sy = intArrayOf(source.y, source.y + corner, source.y + source.h - corner)
    val sw = intArrayOf(corner, source.w - corner * 2, corner)
    val sh = intArrayOf(corner, source.h - corner * 2, corner)
    val dx = floatArrayOf(0f, edge, size.width - edge)
    val dy = floatArrayOf(0f, edge, size.height - edge)
    val dw = floatArrayOf(edge, size.width - edge * 2, edge)
    val dh = floatArrayOf(edge, size.height - edge * 2, edge)
    for (row in 0..2) for (column in 0..2) {
        val slice = if (column == 1 && (!navigation || row == 1)) {
            wood.copy(y = sy[row], h = sh[row])
        } else {
            ArtRect(sx[column], sy[row], sw[column], sh[row])
        }
        art(image, slice, dx[column], dy[row], dw[column], dh[row])
    }
}

fun Modifier.approvedMobilePanel(selected: Boolean = false, navigation: Boolean = false): Modifier = composed {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    drawBehind { panel(atlas, selected, navigation) }
}

@Composable
fun ApprovedMobileBrand(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_carved_brand_reference)
    Canvas(modifier) { art(atlas, ArtRect(65, 104, 724, 145), 0f, 0f, size.width, size.height) }
}

// Registration against the owner's approved carved reference, in source pixels.
internal const val CARVED_MEDALLION_ASPECT = 720f / 712f
internal const val CARVED_EYE_LEFT = 148f / 712f
internal const val CARVED_EYE_TOP = 81f / 720f
internal const val CARVED_EYE_DIAMETER = 418f / 712f

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

package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import kotlin.math.roundToInt

/** Unmodified approved artwork. Only ornament/wood regions are sampled; UI text stays live. */
private data class ArtRect(val x: Int, val y: Int, val w: Int, val h: Int)

private fun DrawScope.art(image: ImageBitmap, source: ArtRect, x: Float, y: Float, w: Float, h: Float) {
    if (w <= 0f || h <= 0f) return
    drawImage(image, IntOffset(source.x, source.y), IntSize(source.w, source.h),
        IntOffset(x.roundToInt(), y.roundToInt()), IntSize(w.roundToInt().coerceAtLeast(1), h.roundToInt().coerceAtLeast(1)))
}

private fun DrawScope.panel(image: ImageBitmap, selected: Boolean, navigation: Boolean) {
    val source = when {
        navigation -> ArtRect(13, 1632, 826, 186)
        selected -> ArtRect(87, 1174, 374, 87)
        else -> ArtRect(72, 252, 708, 126)
    }
    val corner = if (navigation) 38 else if (selected) 20 else 32
    val edge = minOf(if (navigation) 16.dp.toPx() else 13.dp.toPx(), size.width / 3, size.height / 3)
    val wood = if (selected) ArtRect(102, 1187, 24, 28) else ArtRect(125, 281, 91, 63)
    art(image, wood, edge / 2, edge / 2, size.width - edge, size.height - edge)
    val sx = intArrayOf(source.x, source.x + corner, source.x + source.w - corner)
    val sy = intArrayOf(source.y, source.y + corner, source.y + source.h - corner)
    val sw = intArrayOf(corner, source.w - corner * 2, corner)
    val sh = intArrayOf(corner, source.h - corner * 2, corner)
    val dx = floatArrayOf(0f, edge, size.width - edge)
    val dy = floatArrayOf(0f, edge, size.height - edge)
    val dw = floatArrayOf(edge, size.width - edge * 2, edge)
    val dh = floatArrayOf(edge, size.height - edge * 2, edge)
    for (row in 0..2) for (column in 0..2) {
        if (row == 1 && column == 1) continue
        art(image, ArtRect(sx[column], sy[row], sw[column], sh[row]), dx[column], dy[row], dw[column], dh[row])
    }
}

fun Modifier.approvedMobilePanel(selected: Boolean = false, navigation: Boolean = false): Modifier = composed {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    drawBehind { panel(atlas, selected, navigation) }
}

@Composable
fun ApprovedMobileBrand(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    Canvas(modifier) { art(atlas, ArtRect(65, 82, 724, 150), 0f, 0f, size.width, size.height) }
}

@Composable
fun ApprovedMobileEyeFrame(modifier: Modifier = Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    Canvas(modifier) { art(atlas, ArtRect(151, 510, 550, 550), 0f, 0f, size.width, size.height) }
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
            art(atlas, ArtRect(72, 0, 708, 82), side, 0f, size.width - side * 2, 32.dp.toPx())
        }
    }
}

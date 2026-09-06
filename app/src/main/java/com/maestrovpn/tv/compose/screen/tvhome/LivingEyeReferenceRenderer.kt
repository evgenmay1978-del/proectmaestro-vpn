package com.maestrovpn.tv.compose.screen.tvhome

import android.graphics.Paint
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import kotlin.math.roundToInt

/** Reusable per-composition buffers; only vertices change during the existing blink clock. */
internal class ReferenceEyeMesh {
    val vertices = FloatArray((GRID + 1) * (GRID + 1) * 2)
    val paint = Paint(Paint.ANTI_ALIAS_FLAG or Paint.FILTER_BITMAP_FLAG)
    val openUpper = FloatArray(GRID + 1)
    val openLower = FloatArray(GRID + 1)
    val currentUpper = FloatArray(GRID + 1)
    val currentLower = FloatArray(GRID + 1)
    val marginUpper = FloatArray(REFERENCE_EYE_CONTROLS.size)
    val marginLower = FloatArray(REFERENCE_EYE_CONTROLS.size)

    init {
        for (column in 0..GRID) {
            val margin = referenceEyeMargin(column * REFERENCE_EYE_SIZE / GRID, 0f)
            openUpper[column] = margin.upper
            openLower[column] = margin.lower
        }
    }

    fun prepare(phase: Float) {
        for (column in 0..GRID) {
            val upper = openUpper[column]
            val lower = openLower[column]
            val seam = upper * 0.25f + lower * 0.75f
            currentUpper[column] = upper + (seam - upper) * phase
            currentLower[column] = lower + (seam - lower) * phase
        }
        REFERENCE_EYE_CONTROLS.forEachIndexed { index, source ->
            val seam = source.upper * 0.25f + source.lower * 0.75f
            marginUpper[index] = source.upper + (seam - source.upper) * phase
            marginLower[index] = source.lower + (seam - source.lower) * phase
        }
    }

    companion object { const val GRID = 64 }
}

internal fun DrawScope.drawReferenceEye(
    lids: ImageBitmap,
    sclera: ImageBitmap,
    iris: ImageBitmap,
    catchlight: ImageBitmap,
    mesh: ReferenceEyeMesh,
    closure: Float,
    gazeX: Float,
    gazeY: Float,
    pupilScale: Float,
) {
    val phase = closure.coerceIn(0f, 1f)
    mesh.prepare(phase)
    val radius = minOf(size.width, size.height) * REFERENCE_EYE_SOCKET_FRACTION
    val scale = radius * 2f / REFERENCE_EYE_SIZE
    val left = size.width / 2f - radius
    val top = size.height / 2f - radius
    fun point(x: Float, y: Float) = Offset(left + x * scale, top + y * scale)
    fun layer(image: ImageBitmap, x: Float, y: Float, width: Float, height: Float) {
        drawImage(image, srcOffset = IntOffset.Zero, srcSize = IntSize(image.width, image.height),
            dstOffset = IntOffset((left + x * scale).roundToInt(), (top + y * scale).roundToInt()),
            dstSize = IntSize((width * scale).roundToInt().coerceAtLeast(1),
                (height * scale).roundToInt().coerceAtLeast(1)), filterQuality = FilterQuality.High)
    }
    fun eyeBand(start: Float, end: Float): Path {
        fun control(index: Int, fraction: Float): Offset = point(
            REFERENCE_EYE_CONTROLS[index].x,
            mesh.marginUpper[index] + (mesh.marginLower[index] - mesh.marginUpper[index]) * fraction,
        )
        return Path().apply {
            val first = control(0, start)
            moveTo(first.x, first.y)
            fun segment(a: Int, b: Int, c: Int, fraction: Float) {
                val p1 = control(a, fraction)
                val p2 = control(b, fraction)
                val p3 = control(c, fraction)
                cubicTo(p1.x, p1.y, p2.x, p2.y, p3.x, p3.y)
            }
            segment(1, 2, 3, start)
            segment(4, 5, 6, start)
            segment(5, 4, 3, end)
            segment(2, 1, 0, end)
            close()
        }
    }
    val socket = Path().apply { addOval(Rect(left, top, left + radius * 2f, top + radius * 2f)) }
    clipPath(socket) {
        if (livingEyeRenderPolicy(phase).eyeLayersEnabled) {
            val aperture = eyeBand(0f, 1f)
            clipPath(aperture) {
                layer(sclera, 0f, 0f, REFERENCE_EYE_SIZE, REFERENCE_EYE_SIZE)
                // Preserve the old physical gaze amplitude at the new source resolution.
                val dx = gazeX * 0.42f
                val dy = gazeY * 0.42f
                layer(iris, 113f + dx, 111f + dy, 144f, 144f)
                val pupilCenter = point(185f + dx, 183f + dy)
                val pupilRadius = (25.5f * pupilScale + 1.8f) * scale
                drawCircle(Brush.radialGradient(
                    0f to Color(0xFF010605), 0.86f to Color(0xFF020807),
                    1f to Color(0x000A150C), center = pupilCenter, radius = pupilRadius),
                    radius = pupilRadius, center = pupilCenter)
                layer(catchlight, 161f + dx * 0.08f, 134f + dy * 0.08f, 48f, 48f)
                // The upper lid casts a soft contact shadow over the whole globe,
                // including the iris and corneal reflection, as it closes.
                repeat(8) { band ->
                    val shadow = eyeBand(band / 8f * 0.22f, (band + 1) / 8f * 0.22f)
                    val fade = 1f - (band + 0.5f) / 8f
                    drawPath(shadow, Color(0xFF020905).copy(alpha = (0.12f + phase * 0.35f) * fade * fade))
                }
            }
        }
        // One complete textured surface, never an open/closed crossfade. Native
        // bitmap mesh preserves texture registration while both actual lids meet.
        var index = 0
        for (row in 0..ReferenceEyeMesh.GRID) {
            val y = row * REFERENCE_EYE_SIZE / ReferenceEyeMesh.GRID
            for (column in 0..ReferenceEyeMesh.GRID) {
                val x = column * REFERENCE_EYE_SIZE / ReferenceEyeMesh.GRID
                mesh.vertices[index++] = left + x * scale
                mesh.vertices[index++] = top + referenceEyeWarpBetween(y,
                    mesh.openUpper[column], mesh.openLower[column],
                    mesh.currentUpper[column], mesh.currentLower[column]) * scale
            }
        }
        drawIntoCanvas { canvas ->
            canvas.nativeCanvas.drawBitmapMesh(lids.asAndroidBitmap(), ReferenceEyeMesh.GRID,
                ReferenceEyeMesh.GRID, mesh.vertices, 0, null, 0, mesh.paint)
        }
        if (phase > 0.9f) {
            val seam = Path().apply {
                fun control(index: Int): Offset {
                    val source = REFERENCE_EYE_CONTROLS[index]
                    return point(source.x, source.upper * 0.25f + source.lower * 0.75f)
                }
                val first = control(0)
                moveTo(first.x, first.y)
                for (offset in listOf(0, 3)) {
                    val a = control(offset + 1)
                    val b = control(offset + 2)
                    val c = control(offset + 3)
                    cubicTo(a.x, a.y, b.x, b.y, c.x, c.y)
                }
            }
            drawPath(seam, Color(0xFF06130B).copy(alpha = ((phase - 0.9f) * 10f).coerceIn(0f, 1f)),
                style = Stroke(width = 0.8f * scale))
        }
    }
}

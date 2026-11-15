package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathOperation
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.LineHeightStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.sin

/**
 * Country flags of the server rows as a real circle.
 *
 * Every flag is drawn here as vector marks on the unit square that the badge crops to a circle, so
 * all of them fill the ring the same way and sit exactly in its centre. There is no emoji fallback
 * any more: an emoji flag is a wavy rectangle with transparent margins, and drawn as text it sat
 * off-centre (font padding, sp growing with the system font size) and left uneven edges inside the
 * ring (owner's screen, 15.11.2026). A country without a drawing shows its ISO code in the ring.
 *
 * Coordinates are fractions of the badge side: (0, 0) top-left, (1, 1) bottom-right; radii and
 * widths are fractions of the side too. Emblems are simplified to what reads at 22–34 dp.
 */

private sealed interface Mark

private class Box01(val x: Float, val y: Float, val w: Float, val h: Float, val color: Color) : Mark

private class Disc(val cx: Float, val cy: Float, val r: Float, val color: Color) : Mark

private class Ring(val cx: Float, val cy: Float, val r: Float, val width: Float, val color: Color) : Mark

/** Closed polygon x0, y0, x1, y1, …; filled, or outlined when [strokeWidth] > 0. */
private class Poly(val points: FloatArray, val color: Color, val strokeWidth: Float = 0f) : Mark

private class Line01(val x1: Float, val y1: Float, val x2: Float, val y2: Float, val width: Float, val color: Color) : Mark

/** Star with [points] tips; [rotation] is the angle of the first tip in degrees (-90 = up). */
private class Star(
    val cx: Float,
    val cy: Float,
    val r: Float,
    val color: Color,
    val points: Int = 5,
    val inner: Float = 0.382f,
    val rotation: Float = -90f,
) : Mark

/** A disc minus an offset disc: the crescent keeps whatever background lies under it. */
private class Crescent(
    val cx: Float,
    val cy: Float,
    val r: Float,
    val cutX: Float,
    val cutY: Float,
    val cutR: Float,
    val color: Color,
) : Mark

/** Another flag drawn into a canton (the Union Jack of Australia and New Zealand). */
private class Inset(val code: String, val x: Float, val y: Float, val w: Float, val h: Float) : Mark

private class FlagDesign(
    val bands: List<Color> = emptyList(),
    val weights: List<Float>? = null,
    val vertical: Boolean = false,
    val marks: List<Mark> = emptyList(),
)

private fun c(argb: Long) = Color(argb)

private val WHITE = Color.White
private val BLACK = Color(0xFF000000)

/** Horizontal bands of equal height. */
private fun h(vararg colors: Long, marks: List<Mark> = emptyList()) = FlagDesign(colors.map(::c), marks = marks)

/** Vertical bands of equal width. */
private fun hv(vararg colors: Long, marks: List<Mark> = emptyList()) =
    FlagDesign(colors.map(::c), vertical = true, marks = marks)

/** Bands with their own weights. */
private fun bands(colors: List<Long>, weights: List<Float>, marks: List<Mark> = emptyList(), vertical: Boolean = false) =
    FlagDesign(colors.map(::c), weights, vertical, marks)

private fun plain(color: Long, marks: List<Mark>) = FlagDesign(listOf(c(color)), marks = marks)

/** [count] alternating horizontal stripes starting with [first], plus [marks]. */
private fun stripes(count: Int, first: Long, second: Long, vararg marks: Mark) =
    FlagDesign(List(count) { if (it % 2 == 0) c(first) else c(second) }, marks = marks.toList())

/** Nordic cross; [inner] adds the narrow cross of Norway, Iceland and the Faroe Islands. */
private fun nordic(background: Long, cross: Long, inner: Long? = null): FlagDesign {
    val wide = if (inner == null) 0.16f else 0.22f
    val marks = mutableListOf<Mark>(
        Box01(0.30f - wide / 2f, 0f, wide, 1f, c(cross)),
        Box01(0f, 0.5f - wide / 2f, 1f, wide, c(cross)),
    )
    if (inner != null) {
        val thin = 0.11f
        marks += Box01(0.30f - thin / 2f, 0f, thin, 1f, c(inner))
        marks += Box01(0f, 0.5f - thin / 2f, 1f, thin, c(inner))
    }
    return plain(background, marks)
}

private fun poly(vararg xy: Float, color: Color, stroke: Float = 0f) = Poly(xy, color, stroke)

private fun List<Pair<Float, Float>>.flat(): FloatArray = flatMap { (x, y) -> listOf(x, y) }.toFloatArray()

/** Points of a regular figure around (cx, cy), the first one at [startDeg]. */
private fun ringPoints(cx: Float, cy: Float, r: Float, count: Int, startDeg: Float = -90f): List<Pair<Float, Float>> =
    List(count) { i ->
        val angle = (startDeg + 360f * i / count) * PI.toFloat() / 180f
        (cx + r * cos(angle)) to (cy + r * sin(angle))
    }

/** White serrated hoist of Qatar and Bahrain with [teeth] points. */
private fun serrated(teeth: Int): FloatArray {
    val points = mutableListOf(0f, 0f, 0.30f, 0f)
    for (i in 0 until teeth) {
        val top = i.toFloat() / teeth
        points += listOf(0.42f, top + 0.5f / teeth, 0.30f, top + 1f / teeth)
    }
    points += listOf(0f, 1f)
    return points.toFloatArray()
}

private val UNION_JACK = plain(
    0xFF012169,
    listOf(
        Line01(0f, 0f, 1f, 1f, 0.20f, WHITE),
        Line01(1f, 0f, 0f, 1f, 0.20f, WHITE),
        Line01(0f, 0f, 1f, 1f, 0.07f, c(0xFFC8102E)),
        Line01(1f, 0f, 0f, 1f, 0.07f, c(0xFFC8102E)),
        Box01(0.40f, 0f, 0.20f, 1f, WHITE),
        Box01(0f, 0.40f, 1f, 0.20f, WHITE),
        Box01(0.44f, 0f, 0.12f, 1f, c(0xFFC8102E)),
        Box01(0f, 0.44f, 1f, 0.12f, c(0xFFC8102E)),
    ),
)

/**
 * Stars of the US canton. The circle hides the top-left corner of the canton, so the grid sits in
 * the part the ring leaves visible instead of being cut in half by it.
 */
private val US_STARS: List<Mark> = buildList {
    for (row in 0 until 4) {
        for (column in 0 until 4) {
            val x = 0.17f + column * 0.085f + (if (row % 2 == 1) 0.04f else 0f)
            val y = 0.14f + row * 0.10f
            val dx = x - 0.5f
            val dy = y - 0.5f
            if (dx * dx + dy * dy < 0.43f * 0.43f) add(Star(x, y, 0.027f, WHITE))
        }
    }
}

private val MAPLE_LEAF: FloatArray = run {
    val right = listOf(
        0.50f to 0.00f, 0.58f to 0.15f, 0.68f to 0.10f, 0.64f to 0.38f, 0.79f to 0.22f, 0.83f to 0.31f,
        0.97f to 0.27f, 0.91f to 0.46f, 1.00f to 0.51f, 0.77f to 0.67f, 0.81f to 0.77f, 0.54f to 0.72f,
        0.545f to 0.98f, 0.50f to 0.98f,
    )
    val left = right.drop(1).dropLast(1).reversed().map { (x, y) -> (1f - x) to y }
    (right + (0.455f to 0.98f) + left).map { (x, y) -> (0.27f + x * 0.46f) to (0.22f + y * 0.56f) }.flat()
}

private val KOREA: List<Mark> = listOf<Mark>(
    Disc(0.5f, 0.5f, 0.22f, c(0xFF0047A0)),
    Poly(ringPoints(0.5f, 0.5f, 0.22f, 24, 180f).take(13).flat(), c(0xFFCD2E3A)),
    Disc(0.39f, 0.5f, 0.11f, c(0xFFCD2E3A)),
    Disc(0.61f, 0.5f, 0.11f, c(0xFF0047A0)),
) + listOf(0.21f to 0.27f, 0.79f to 0.27f, 0.21f to 0.73f, 0.79f to 0.73f).flatMap { (x, y) ->
    List(3) { i -> Box01(x - 0.065f, y - 0.05f + i * 0.04f, 0.13f, 0.022f, BLACK) }
}

private val FLAGS: Map<String, FlagDesign> = mapOf(
    // Europe
    "ES" to bands(listOf(0xFFC60B1E, 0xFFF1BF00, 0xFFC60B1E), listOf(1f, 2f, 1f)),
    "NL" to h(0xFFAE1C28, 0xFFFFFFFF, 0xFF21468B),
    "LU" to h(0xFFEF3340, 0xFFFFFFFF, 0xFF00A2E1),
    "RU" to h(0xFFFFFFFF, 0xFF0033A0, 0xFFDA291C),
    "AT" to h(0xFFED2939, 0xFFFFFFFF, 0xFFED2939),
    "EE" to h(0xFF0072CE, 0xFF000000, 0xFFFFFFFF),
    "DE" to h(0xFF000000, 0xFFDD0000, 0xFFFFCE00),
    "LV" to bands(listOf(0xFF9E3039, 0xFFFFFFFF, 0xFF9E3039), listOf(2f, 1f, 2f)),
    "LT" to h(0xFFFDB913, 0xFF006A44, 0xFFC1272D),
    "PL" to h(0xFFFFFFFF, 0xFFDC143C),
    "SK" to h(0xFFFFFFFF, 0xFF0B4EA2, 0xFFEE1C25),
    "SI" to h(0xFFFFFFFF, 0xFF005DA4, 0xFFED1C24),
    "HU" to h(0xFFCE2939, 0xFFFFFFFF, 0xFF477050),
    "BG" to h(0xFFFFFFFF, 0xFF00966E, 0xFFD62612),
    "UA" to h(0xFF0057B7, 0xFFFFD700),
    "BY" to bands(
        listOf(0xFFC8313E, 0xFF4AA657), listOf(2f, 1f),
        listOf<Mark>(Box01(0f, 0f, 0.13f, 1f, WHITE)) + listOf(0.30f, 0.50f, 0.70f).map { y ->
            poly(0.065f, y - 0.07f, 0.11f, y, 0.065f, y + 0.07f, 0.02f, y, color = c(0xFFC8313E))
        },
    ),
    "RS" to h(
        0xFFC6363C, 0xFF0C4076, 0xFFFFFFFF,
        marks = listOf(
            Box01(0.27f, 0.28f, 0.15f, 0.22f, c(0xFFC6363C)),
            Box01(0.33f, 0.30f, 0.03f, 0.18f, WHITE),
            Box01(0.29f, 0.37f, 0.11f, 0.03f, WHITE),
        ),
    ),
    "HR" to h(
        0xFFFF0000, 0xFFFFFFFF, 0xFF171796,
        marks = buildList {
            for (row in 0 until 5) {
                for (column in 0 until 5) {
                    val color = if ((row + column) % 2 == 0) c(0xFFFF0000) else WHITE
                    add(Box01(0.40f + column * 0.04f, 0.30f + row * 0.04f, 0.04f, 0.04f, color))
                }
            }
        },
    ),
    "BA" to plain(
        0xFF002395,
        listOf<Mark>(poly(0.27f, 0f, 0.75f, 0f, 0.75f, 1f, color = c(0xFFFECB00))) +
            List(8) { i -> (0.08f + i * 0.12f).let { t -> Star(0.20f + 0.48f * t, t, 0.035f, WHITE) } },
    ),
    "IT" to hv(0xFF008C45, 0xFFF4F5F0, 0xFFCD212A),
    "FR" to hv(0xFF0055A4, 0xFFFFFFFF, 0xFFEF4135),
    "BE" to hv(0xFF000000, 0xFFFDDA24, 0xFFEF3340),
    "RO" to hv(0xFF002B7F, 0xFFFCD116, 0xFFCE1126),
    "MD" to hv(0xFF0046AE, 0xFFFCD116, 0xFFCC092F),
    "IE" to hv(0xFF169B62, 0xFFFFFFFF, 0xFFFF883E),
    "AD" to hv(0xFF10069F, 0xFFFEDF00, 0xFFD50032),
    "MT" to hv(0xFFFFFFFF, 0xFFCF142B),
    "VA" to hv(0xFFFFE000, 0xFFFFFFFF),
    "SM" to h(0xFFFFFFFF, 0xFF5EB6E4),
    "MC" to h(0xFFCE1126, 0xFFFFFFFF),
    "LI" to h(0xFF002B7F, 0xFFCE1126, marks = listOf(Disc(0.30f, 0.25f, 0.07f, c(0xFFFFD83D)))),
    "PT" to bands(
        listOf(0xFF006600, 0xFFFF0000), listOf(2f, 3f),
        listOf(Disc(0.40f, 0.5f, 0.17f, c(0xFFFFCC00)), Disc(0.40f, 0.5f, 0.11f, c(0xFFFF0000)), Disc(0.40f, 0.5f, 0.06f, WHITE)),
        vertical = true,
    ),
    "GR" to stripes(
        9, 0xFF0D5EAF, 0xFFFFFFFF,
        Box01(0f, 0f, 0.5f, 5f / 9f, c(0xFF0D5EAF)),
        Box01(0.195f, 0f, 1f / 9f, 5f / 9f, WHITE),
        Box01(0f, 2f / 9f, 0.5f, 1f / 9f, WHITE),
    ),
    "CH" to plain(0xFFD52B1E, listOf(Box01(0.41f, 0.22f, 0.18f, 0.56f, WHITE), Box01(0.22f, 0.41f, 0.56f, 0.18f, WHITE))),
    "CZ" to h(0xFFFFFFFF, 0xFFD7141A, marks = listOf(poly(0f, 0f, 0.55f, 0.5f, 0f, 1f, color = c(0xFF11457E)))),
    "SE" to nordic(0xFF006AA7, 0xFFFECC02),
    "FI" to nordic(0xFFFFFFFF, 0xFF003580),
    "DK" to nordic(0xFFC8102E, 0xFFFFFFFF),
    "NO" to nordic(0xFFBA0C2F, 0xFFFFFFFF, 0xFF00205B),
    "IS" to nordic(0xFF02529C, 0xFFFFFFFF, 0xFFDC1E35),
    "FO" to nordic(0xFFFFFFFF, 0xFF0065BD, 0xFFED2939),
    "GB" to UNION_JACK,
    "GE" to plain(0xFFFFFFFF, buildList {
        add(Box01(0.41f, 0f, 0.18f, 1f, c(0xFFFF0000)))
        add(Box01(0f, 0.41f, 1f, 0.18f, c(0xFFFF0000)))
        for ((x, y) in listOf(0.22f to 0.22f, 0.78f to 0.22f, 0.22f to 0.78f, 0.78f to 0.78f)) {
            add(Box01(x - 0.05f, y - 0.015f, 0.10f, 0.03f, c(0xFFFF0000)))
            add(Box01(x - 0.015f, y - 0.05f, 0.03f, 0.10f, c(0xFFFF0000)))
        }
    }),
    "AM" to h(0xFFD90012, 0xFF0033A0, 0xFFF2A800),
    "AZ" to h(
        0xFF00B5E2, 0xFFEF3340, 0xFF509E2F,
        marks = listOf(
            Crescent(0.47f, 0.5f, 0.12f, 0.50f, 0.5f, 0.10f, WHITE),
            Star(0.60f, 0.5f, 0.055f, WHITE, points = 8, inner = 0.5f),
        ),
    ),
    "TR" to plain(
        0xFFE30A17,
        listOf(Crescent(0.42f, 0.5f, 0.25f, 0.47f, 0.5f, 0.20f, WHITE), Star(0.66f, 0.5f, 0.11f, WHITE, rotation = 180f)),
    ),
    "MK" to plain(
        0xFFD20000,
        ringPoints(0.5f, 0.5f, 0.75f, 8).map { (x, y) ->
            val dx = (y - 0.5f) * 0.12f
            val dy = (0.5f - x) * 0.12f
            poly(0.5f, 0.5f, x - dx, y - dy, x + dx, y + dy, color = c(0xFFFFE600))
        } + Disc(0.5f, 0.5f, 0.12f, c(0xFFFFE600)),
    ),
    "EU" to plain(0xFF003399, ringPoints(0.5f, 0.5f, 0.30f, 12).map { (x, y) -> Star(x, y, 0.045f, c(0xFFFFCC00)) }),
    // Asia and the Middle East
    "KZ" to plain(
        0xFF00AFCA,
        listOf(
            Star(0.5f, 0.42f, 0.25f, c(0xFFFEC50C), points = 32, inner = 0.78f),
            Disc(0.5f, 0.42f, 0.15f, c(0xFFFEC50C)),
            poly(0.24f, 0.70f, 0.5f, 0.62f, 0.76f, 0.70f, 0.5f, 0.78f, color = c(0xFFFEC50C)),
        ),
    ),
    "UZ" to bands(
        listOf(0xFF0099B5, 0xFFCE1126, 0xFFFFFFFF, 0xFFCE1126, 0xFF1EB53A), listOf(10f, 1f, 10f, 1f, 10f),
        listOf<Mark>(Crescent(0.28f, 0.17f, 0.10f, 0.32f, 0.17f, 0.085f, WHITE)) +
            listOf(0.42f to 0.12f, 0.50f to 0.12f, 0.58f to 0.12f, 0.46f to 0.22f, 0.54f to 0.22f).map { (x, y) -> Star(x, y, 0.025f, WHITE) },
    ),
    "KG" to plain(
        0xFFE8112D,
        listOf(
            Star(0.5f, 0.5f, 0.32f, c(0xFFFFEF00), points = 40, inner = 0.72f),
            Disc(0.5f, 0.5f, 0.18f, c(0xFFFFEF00)),
            Ring(0.5f, 0.5f, 0.12f, 0.025f, c(0xFFE8112D)),
        ),
    ),
    "TJ" to bands(
        listOf(0xFFCC0000, 0xFFFFFFFF, 0xFF006600), listOf(2f, 3f, 2f),
        listOf<Mark>(Disc(0.5f, 0.54f, 0.05f, c(0xFFF8C300))) + List(7) { i ->
            val angle = (200f + i * 23.3f) * PI.toFloat() / 180f
            Star(0.5f + 0.13f * cos(angle), 0.54f + 0.13f * sin(angle), 0.022f, c(0xFFF8C300))
        },
    ),
    "MN" to hv(
        0xFFC4272F, 0xFF015197, 0xFFC4272F,
        marks = listOf(Disc(0.165f, 0.33f, 0.045f, c(0xFFF9CF02)), Box01(0.125f, 0.40f, 0.08f, 0.28f, c(0xFFF9CF02))),
    ),
    "CN" to plain(
        0xFFEE1C25,
        listOf(
            Star(0.30f, 0.33f, 0.14f, c(0xFFFFFF00)),
            Star(0.50f, 0.18f, 0.045f, c(0xFFFFFF00), rotation = -60f),
            Star(0.58f, 0.27f, 0.045f, c(0xFFFFFF00), rotation = -80f),
            Star(0.58f, 0.39f, 0.045f, c(0xFFFFFF00), rotation = -100f),
            Star(0.50f, 0.48f, 0.045f, c(0xFFFFFF00), rotation = -120f),
        ),
    ),
    "HK" to plain(
        0xFFDE2910,
        ringPoints(0.5f, 0.5f, 0.13f, 5).map { (x, y) -> Disc(x, y, 0.11f, WHITE) } +
            ringPoints(0.5f, 0.5f, 0.13f, 5).map { (x, y) -> Star(x, y, 0.03f, c(0xFFDE2910)) },
    ),
    "TW" to plain(
        0xFFFE0000,
        listOf(
            Box01(0f, 0f, 0.5f, 0.5f, c(0xFF000095)),
            Star(0.25f, 0.25f, 0.15f, WHITE, points = 12, inner = 0.6f),
            Disc(0.25f, 0.25f, 0.075f, c(0xFF000095)),
            Disc(0.25f, 0.25f, 0.062f, WHITE),
        ),
    ),
    "JP" to plain(0xFFFFFFFF, listOf(Disc(0.5f, 0.5f, 0.28f, c(0xFFBC002D)))),
    "KR" to plain(0xFFFFFFFF, KOREA),
    "VN" to plain(0xFFDA251D, listOf(Star(0.5f, 0.52f, 0.27f, c(0xFFFFFF00)))),
    "TH" to bands(listOf(0xFFA51931, 0xFFF4F5F8, 0xFF2D2A4A, 0xFFF4F5F8, 0xFFA51931), listOf(1f, 1f, 2f, 1f, 1f)),
    "LA" to bands(listOf(0xFFCE1126, 0xFF002868, 0xFFCE1126), listOf(1f, 2f, 1f), listOf(Disc(0.5f, 0.5f, 0.16f, WHITE))),
    "MM" to h(0xFFFECB00, 0xFF34B233, 0xFFEA2839, marks = listOf(Star(0.5f, 0.55f, 0.30f, WHITE))),
    "MY" to stripes(
        14, 0xFFCC0001, 0xFFFFFFFF,
        Box01(0f, 0f, 0.55f, 8f / 14f, c(0xFF010066)),
        Crescent(0.22f, 0.29f, 0.15f, 0.27f, 0.29f, 0.125f, c(0xFFFFCC00)),
        Star(0.40f, 0.29f, 0.09f, c(0xFFFFCC00), points = 14, inner = 0.45f),
    ),
    "SG" to h(
        0xFFEF3340, 0xFFFFFFFF,
        marks = listOf<Mark>(Crescent(0.27f, 0.27f, 0.14f, 0.32f, 0.27f, 0.125f, WHITE)) +
            ringPoints(0.39f, 0.27f, 0.075f, 5).map { (x, y) -> Star(x, y, 0.03f, WHITE) },
    ),
    "ID" to h(0xFFFF0000, 0xFFFFFFFF),
    "PH" to h(
        0xFF0038A8, 0xFFCE1126,
        marks = listOf(
            poly(0f, 0f, 0.48f, 0.5f, 0f, 1f, color = WHITE),
            Star(0.16f, 0.5f, 0.10f, c(0xFFFCD116), points = 8, inner = 0.55f),
            Disc(0.16f, 0.5f, 0.055f, c(0xFFFCD116)),
        ),
    ),
    "IN" to h(
        0xFFFF9933, 0xFFFFFFFF, 0xFF138808,
        marks = listOf(Ring(0.5f, 0.5f, 0.11f, 0.022f, c(0xFF000080)), Disc(0.5f, 0.5f, 0.025f, c(0xFF000080))),
    ),
    "PK" to plain(
        0xFF01411C,
        listOf(
            Box01(0f, 0f, 0.25f, 1f, WHITE),
            Crescent(0.60f, 0.52f, 0.22f, 0.66f, 0.46f, 0.19f, WHITE),
            Star(0.69f, 0.39f, 0.07f, WHITE, rotation = -55f),
        ),
    ),
    "BD" to plain(0xFF006A4E, listOf(Disc(0.45f, 0.5f, 0.25f, c(0xFFF42A41)))),
    "IL" to plain(
        0xFFFFFFFF,
        listOf(
            Box01(0f, 0.16f, 1f, 0.10f, c(0xFF0038B8)),
            Box01(0f, 0.74f, 1f, 0.10f, c(0xFF0038B8)),
            poly(0.5f, 0.33f, 0.64f, 0.575f, 0.36f, 0.575f, color = c(0xFF0038B8), stroke = 0.03f),
            poly(0.5f, 0.67f, 0.64f, 0.425f, 0.36f, 0.425f, color = c(0xFF0038B8), stroke = 0.03f),
        ),
    ),
    "AE" to h(0xFF00732F, 0xFFFFFFFF, 0xFF000000, marks = listOf(Box01(0f, 0f, 0.27f, 1f, c(0xFFFF0000)))),
    "KW" to h(
        0xFF007A3D, 0xFFFFFFFF, 0xFFCE1126,
        marks = listOf(poly(0f, 0f, 0.27f, 0.333f, 0.27f, 0.667f, 0f, 1f, color = BLACK)),
    ),
    "QA" to plain(0xFF8A1538, listOf(Poly(serrated(9), WHITE))),
    "BH" to plain(0xFFCE1126, listOf(Poly(serrated(5), WHITE))),
    "JO" to h(
        0xFF000000, 0xFFFFFFFF, 0xFF007A3D,
        marks = listOf(
            poly(0f, 0f, 0.5f, 0.5f, 0f, 1f, color = c(0xFFCE1126)),
            Star(0.17f, 0.5f, 0.06f, WHITE, points = 7, inner = 0.5f),
        ),
    ),
    "PS" to h(0xFF000000, 0xFFFFFFFF, 0xFF007A3D, marks = listOf(poly(0f, 0f, 0.5f, 0.5f, 0f, 1f, color = c(0xFFCE1126)))),
    "LB" to bands(
        listOf(0xFFED1C24, 0xFFFFFFFF, 0xFFED1C24), listOf(1f, 2f, 1f),
        listOf(
            poly(0.5f, 0.30f, 0.66f, 0.62f, 0.34f, 0.62f, color = c(0xFF00A651)),
            Box01(0.47f, 0.62f, 0.06f, 0.08f, c(0xFF00A651)),
        ),
    ),
    "IQ" to h(0xFFCE1126, 0xFFFFFFFF, 0xFF000000, marks = listOf(Box01(0.30f, 0.46f, 0.40f, 0.08f, c(0xFF007A3D)))),
    "SY" to h(
        0xFFCE1126, 0xFFFFFFFF, 0xFF000000,
        marks = listOf(Star(0.33f, 0.5f, 0.08f, c(0xFF007A3D)), Star(0.67f, 0.5f, 0.08f, c(0xFF007A3D))),
    ),
    "YE" to h(0xFFCE1126, 0xFFFFFFFF, 0xFF000000),
    "EG" to h(0xFFCE1126, 0xFFFFFFFF, 0xFF000000, marks = listOf(Disc(0.5f, 0.5f, 0.08f, c(0xFFC09300)))),
    "IR" to h(
        0xFF239F40, 0xFFFFFFFF, 0xFFDA0000,
        marks = listOf(Disc(0.5f, 0.5f, 0.07f, c(0xFFDA0000)), Disc(0.5f, 0.5f, 0.04f, WHITE)),
    ),
    // Africa
    "NG" to hv(0xFF008751, 0xFFFFFFFF, 0xFF008751),
    "GH" to h(0xFFCE1126, 0xFFFCD116, 0xFF006B3F, marks = listOf(Star(0.5f, 0.52f, 0.13f, BLACK))),
    "MA" to plain(
        0xFFC1272D,
        listOf(
            Poly(
                ringPoints(0.5f, 0.52f, 0.22f, 5).let { p -> listOf(p[0], p[2], p[4], p[1], p[3]) }.flat(),
                c(0xFF006233),
                strokeWidth = 0.04f,
            ),
        ),
    ),
    "DZ" to hv(
        0xFF006233, 0xFFFFFFFF,
        marks = listOf(
            Crescent(0.52f, 0.5f, 0.22f, 0.58f, 0.5f, 0.18f, c(0xFFD21034)),
            Star(0.62f, 0.5f, 0.09f, c(0xFFD21034), rotation = 180f),
        ),
    ),
    "TN" to plain(
        0xFFE70013,
        listOf(
            Disc(0.5f, 0.5f, 0.25f, WHITE),
            Crescent(0.48f, 0.5f, 0.19f, 0.53f, 0.5f, 0.15f, c(0xFFE70013)),
            Star(0.56f, 0.5f, 0.09f, c(0xFFE70013), rotation = 180f),
        ),
    ),
    // The Americas and Oceania
    "US" to stripes(13, 0xFFB22234, 0xFFFFFFFF, Box01(0f, 0f, 0.5f, 7f / 13f, c(0xFF3C3B6E)), *US_STARS.toTypedArray()),
    "CA" to bands(listOf(0xFFD80621, 0xFFFFFFFF, 0xFFD80621), listOf(1f, 2f, 1f), listOf(Poly(MAPLE_LEAF, c(0xFFD80621))), vertical = true),
    "MX" to hv(
        0xFF006847, 0xFFFFFFFF, 0xFFCE1126,
        marks = listOf(Disc(0.5f, 0.5f, 0.10f, c(0xFF8C5B2D)), Ring(0.5f, 0.5f, 0.12f, 0.02f, c(0xFF006847))),
    ),
    "BR" to plain(
        0xFF009C3B,
        listOf(
            poly(0.08f, 0.5f, 0.5f, 0.16f, 0.92f, 0.5f, 0.5f, 0.84f, color = c(0xFFFFDF00)),
            Disc(0.5f, 0.5f, 0.20f, c(0xFF002776)),
            Line01(0.31f, 0.46f, 0.69f, 0.54f, 0.035f, WHITE),
        ),
    ),
    "AR" to h(
        0xFF74ACDF, 0xFFFFFFFF, 0xFF74ACDF,
        marks = listOf(
            Star(0.5f, 0.5f, 0.14f, c(0xFFF6B40E), points = 16, inner = 0.6f),
            Disc(0.5f, 0.5f, 0.075f, c(0xFFF6B40E)),
        ),
    ),
    "CL" to h(0xFFFFFFFF, 0xFFD52B1E, marks = listOf(Box01(0f, 0f, 0.42f, 0.5f, c(0xFF0039A6)), Star(0.25f, 0.27f, 0.10f, WHITE))),
    "CO" to bands(listOf(0xFFFCD116, 0xFF003893, 0xFFCE1126), listOf(2f, 1f, 1f)),
    "EC" to bands(listOf(0xFFFFDD00, 0xFF034EA2, 0xFFED1C24), listOf(2f, 1f, 1f), listOf(Disc(0.5f, 0.5f, 0.10f, c(0xFF8C5B2D)))),
    "VE" to h(
        0xFFFFCC00, 0xFF00247D, 0xFFCF142B,
        marks = List(8) { i ->
            val angle = (205f + i * 18.5f) * PI.toFloat() / 180f
            Star(0.5f + 0.2f * cos(angle), 0.66f + 0.2f * sin(angle), 0.03f, WHITE)
        },
    ),
    "PE" to hv(0xFFD91023, 0xFFFFFFFF, 0xFFD91023),
    "BO" to h(0xFFD52B1E, 0xFFF9E300, 0xFF007934),
    "PY" to h(0xFFD52B1E, 0xFFFFFFFF, 0xFF0038A8),
    "UY" to stripes(
        9, 0xFFFFFFFF, 0xFF0038A8,
        Box01(0f, 0f, 0.45f, 5f / 9f, WHITE),
        Star(0.24f, 0.28f, 0.13f, c(0xFFFCD116), points = 16, inner = 0.6f),
        Disc(0.24f, 0.28f, 0.07f, c(0xFFFCD116)),
    ),
    "CR" to bands(listOf(0xFF002B7F, 0xFFFFFFFF, 0xFFCE1126, 0xFFFFFFFF, 0xFF002B7F), listOf(1f, 1f, 2f, 1f, 1f)),
    "CU" to stripes(
        5, 0xFF002A8F, 0xFFFFFFFF,
        poly(0f, 0f, 0.5f, 0.5f, 0f, 1f, color = c(0xFFCF142B)),
        Star(0.17f, 0.5f, 0.09f, WHITE),
    ),
    "AU" to plain(
        0xFF00008B,
        listOf(
            Inset("GB", 0f, 0f, 0.5f, 0.5f),
            Star(0.25f, 0.76f, 0.10f, WHITE, points = 7, inner = 0.45f),
            Star(0.75f, 0.24f, 0.05f, WHITE, points = 7, inner = 0.45f),
            Star(0.62f, 0.47f, 0.05f, WHITE, points = 7, inner = 0.45f),
            Star(0.87f, 0.42f, 0.05f, WHITE, points = 7, inner = 0.45f),
            Star(0.75f, 0.80f, 0.05f, WHITE, points = 7, inner = 0.45f),
        ),
    ),
    "NZ" to plain(
        0xFF012169,
        listOf<Mark>(Inset("GB", 0f, 0f, 0.5f, 0.5f)) +
            listOf(0.75f to 0.24f, 0.63f to 0.47f, 0.86f to 0.42f, 0.75f to 0.78f).flatMap { (x, y) ->
                listOf(Star(x, y, 0.065f, WHITE), Star(x, y, 0.045f, c(0xFFC8102E)))
            },
    ),
)

/** ISO codes the badge draws as a flag; any other code is shown as text in the same ring. */
internal val DRAWN_FLAG_CODES: Set<String> get() = FLAGS.keys

private fun DrawScope.drawFlag(design: FlagDesign) {
    val w = size.width
    val h = size.height
    if (design.bands.isNotEmpty()) {
        val weights = design.weights ?: List(design.bands.size) { 1f }
        val total = weights.sum()
        var position = 0f
        design.bands.forEachIndexed { index, color ->
            // +0.5 px overlap so no hairline of the backdrop shows between two bands.
            if (design.vertical) {
                val bw = w * weights[index] / total
                drawRect(color, Offset(position, 0f), Size(bw + 0.5f, h))
                position += bw
            } else {
                val bh = h * weights[index] / total
                drawRect(color, Offset(0f, position), Size(w, bh + 0.5f))
                position += bh
            }
        }
    }
    for (mark in design.marks) drawMark(mark)
}

private fun DrawScope.drawMark(mark: Mark) {
    val w = size.width
    val h = size.height
    when (mark) {
        is Box01 -> drawRect(mark.color, Offset(mark.x * w, mark.y * h), Size(mark.w * w, mark.h * h))
        is Disc -> drawCircle(mark.color, mark.r * w, Offset(mark.cx * w, mark.cy * h))
        is Ring -> drawCircle(mark.color, mark.r * w, Offset(mark.cx * w, mark.cy * h), style = Stroke(mark.width * w))
        is Line01 -> drawLine(mark.color, Offset(mark.x1 * w, mark.y1 * h), Offset(mark.x2 * w, mark.y2 * h), mark.width * w)
        is Poly -> {
            val path = Path()
            var i = 0
            while (i + 1 < mark.points.size) {
                val x = mark.points[i] * w
                val y = mark.points[i + 1] * h
                if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
                i += 2
            }
            path.close()
            if (mark.strokeWidth > 0f) drawPath(path, mark.color, style = Stroke(mark.strokeWidth * w)) else drawPath(path, mark.color)
        }
        is Star -> {
            val path = Path()
            for (i in 0 until mark.points * 2) {
                val radius = if (i % 2 == 0) mark.r else mark.r * mark.inner
                val angle = (mark.rotation + 180f * i / mark.points) * PI.toFloat() / 180f
                val x = (mark.cx + radius * cos(angle)) * w
                val y = (mark.cy + radius * sin(angle)) * h
                if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
            }
            path.close()
            drawPath(path, mark.color)
        }
        is Crescent -> {
            val outer = Path().apply { addOval(Rect(Offset(mark.cx * w, mark.cy * h), mark.r * w)) }
            val cut = Path().apply { addOval(Rect(Offset(mark.cutX * w, mark.cutY * h), mark.cutR * w)) }
            drawPath(Path.combine(PathOperation.Difference, outer, cut), mark.color)
        }
        is Inset -> {
            val design = FLAGS[mark.code] ?: return
            translate(mark.x * w, mark.y * h) {
                withTransform({
                    scale(mark.w, mark.h, Offset.Zero)
                    clipRect(0f, 0f, w, h)
                }) { drawFlag(design) }
            }
        }
    }
}

/**
 * Round country badge of a server row: the flag fills the whole circle, the golden ring sits on the
 * rim. A country without a drawing shows its ISO code in the same ring; a row without a country
 * (or with a pictograph that is not a flag) keeps its role icon.
 */
@Composable
fun CountryFlagBadge(
    flag: String,
    icon: ImageVector,
    modifier: Modifier = Modifier,
    size: Dp = 30.dp,
) {
    val code = flagCountryCode(flag)
    val design = FLAGS[code]
    Box(modifier.size(size).clip(CircleShape), contentAlignment = Alignment.Center) {
        when {
            design != null -> Canvas(Modifier.fillMaxSize()) { drawFlag(design) }
            code.isNotEmpty() -> Box(
                Modifier.fillMaxSize().background(Color(0xFF2A1C10)),
                contentAlignment = Alignment.Center,
            ) {
                // Sized in dp, not sp: the system font scale must not push the code out of the ring.
                val fontSize = with(LocalDensity.current) { (size * 0.36f).toSp() }
                Text(
                    code,
                    color = PremiumGold,
                    maxLines = 1,
                    softWrap = false,
                    style = TextStyle(
                        fontSize = fontSize,
                        fontWeight = FontWeight.Bold,
                        lineHeight = fontSize,
                        lineHeightStyle = LineHeightStyle(LineHeightStyle.Alignment.Center, LineHeightStyle.Trim.Both),
                    ),
                )
            }
            else -> Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(size * 0.8f))
        }
        Box(Modifier.fillMaxSize().border(1.dp, PremiumGold.copy(alpha = 0.85f), CircleShape))
    }
}

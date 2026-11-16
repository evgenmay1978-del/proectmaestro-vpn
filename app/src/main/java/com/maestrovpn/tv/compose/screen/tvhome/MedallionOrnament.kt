package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.lerp
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.sin

/**
 * Декор вокруг медальона — «роза ветров» (16.11.2026, владелец: «не нравятся пики вокруг
 * медальона и пульсации… сделай эффектнее»). Рисуется кодом (векторно), растров нет.
 *
 * Состав: четыре гранёных клинка по осям с изумрудом в оправе, четыре тонких клинка по
 * диагоналям, бусинный пояс сразу за кольцом и тонкие сужающиеся лучи. У каждого клинка две
 * грани — освещённая (свет сверху слева) и теневая — и световое ребро: металл объёмный без
 * всякого свечения.
 *
 * Слой лежит ПОД медальоном и начинается за диском и плашками камней ([ORNAMENT_CLEAR]).
 *
 * Свет НЕ пульсирует (правило владельца 10.09.2026: световая пульсация запрещена; прежние
 * «дыхание» 2,4 с и ореол убраны). Движение одно и разовое — смена состояния:
 *  - подключение: металл разгорается из тёмной бронзы в золото от кольца наружу — сначала
 *    бусины, затем осевые клинки, диагональные, и последними прорастают лучи (~1,1 с);
 *  - отключение: всё ровно гаснет в бронзу (0,6 с). Дальше картинка статична.
 *
 * @param bottomLimitPx верх подписи статуса внутри бокса героя: декор ниже не рисуется.
 */
@Composable
internal fun MedallionOrnament(
    active: Boolean,
    medallionLeftPx: Float,
    medallionTopPx: Float,
    medallionSizePx: Float,
    bottomLimitPx: Float,
    modifier: Modifier = Modifier,
) {
    val progress by animateFloatAsState(
        targetValue = if (active) 1f else 0f,
        animationSpec = if (active) tween(IGNITE_MS, easing = LinearOutSlowInEasing) else tween(FADE_OUT_MS, easing = FastOutSlowInEasing),
        label = "ornamentIgnite",
    )
    Canvas(modifier) {
        val layout = medallionOrnamentLayout(
            width = size.width,
            height = size.height,
            medallionLeft = medallionLeftPx,
            medallionTop = medallionTopPx,
            medallionSize = medallionSizePx,
            bottomLimit = bottomLimitPx,
            margin = 1.5f * density,
        )
        val stages = ornamentStages(progress)
        clipRect(layout.clipLeft, layout.clipTop, layout.clipRight, layout.clipBottom) {
            drawRays(layout, stages.rays)
            drawDiagonalBlades(layout, stages.diagonals)
            drawAxisBlades(layout, stages.axes)
            drawBeads(layout, stages.beads)
        }
    }
}

private const val IGNITE_MS = 1100
private const val FADE_OUT_MS = 600

/** Уровни яркости слоёв (0 — бронза, 1 — золото) для общего прогресса разгорания. */
internal data class OrnamentStages(val beads: Float, val axes: Float, val diagonals: Float, val rays: Float)

/**
 * Разгорание от кольца наружу. При progress = 0 и 1 все слои одинаковы (0 или 1): декор в покое
 * всегда в одном из двух согласованных состояний, а не «наполовину».
 */
internal fun ornamentStages(progress: Float): OrnamentStages {
    fun stage(delay: Float, span: Float) = ((progress - delay) / span).coerceIn(0f, 1f)
    return OrnamentStages(
        beads = stage(0.00f, 0.40f),
        axes = stage(0.15f, 0.45f),
        diagonals = stage(0.30f, 0.45f),
        rays = stage(0.45f, 0.55f),
    )
}

private class Metal(val hi: Color, val mid: Color, val low: Color, val edge: Color)

// ACTIVE: полированное старое золото; INACTIVE: потухшая тёмная бронза.
private val GoldHi = Color(0xFFFFE9A8)
private val GoldMid = Color(0xFFD9A846)
private val GoldLow = Color(0xFF8A5C1A)
private val GoldEdge = Color(0xFF3E270B)
private val BronzeHi = Color(0xFF5B4630)
private val BronzeMid = Color(0xFF443321)
private val BronzeLow = Color(0xFF2E2216)
private val BronzeEdge = Color(0xFF1C140B)

private val EmeraldLight = Color(0xFF9DFFC9)
private val EmeraldBase = Color(0xFF17C25A)
private val EmeraldDark = Color(0xFF054A23)
private val EmeraldDeadLight = Color(0xFF173F29)
private val EmeraldDeadBase = Color(0xFF0B2516)
private val EmeraldDeadDark = Color(0xFF05140C)
private val RayGold = Color(0xFFFFD27A)
private val RayBronze = Color(0xFF6B5434)

private fun metal(t: Float) = Metal(
    hi = lerp(BronzeHi, GoldHi, t),
    mid = lerp(BronzeMid, GoldMid, t),
    low = lerp(BronzeLow, GoldLow, t),
    edge = lerp(BronzeEdge, GoldEdge, t),
)

/** Направление луча из центра: угол в градусах экрана (0 — вправо, 90 — вниз). */
private fun dir(deg: Float): Offset {
    val a = deg * PI.toFloat() / 180f
    return Offset(cos(a), sin(a))
}

/**
 * Гранёный клинок вдоль [d] от [start] до [end] (расстояния от центра): узкое основание, плечо
 * и острие. Две грани по обе стороны ребра: освещённая (к свету сверху слева) светлее.
 */
private fun DrawScope.drawBlade(
    l: OrnamentLayout,
    d: Offset,
    start: Float,
    end: Float,
    baseHalf: Float,
    shoulderAt: Float,
    shoulderHalf: Float,
    m: Metal,
) {
    if (end - start < shoulderAt + 0.04f * l.r) return
    val c = Offset(l.cx, l.cy)
    val p = Offset(-d.y, d.x) // перпендикуляр
    val base = c + d * start
    val shoulder = c + d * (start + shoulderAt)
    val tip = c + d * end
    val baseA = base + p * baseHalf
    val baseB = base - p * baseHalf
    val shA = shoulder + p * shoulderHalf
    val shB = shoulder - p * shoulderHalf
    // Какая сторона смотрит на свет (сверху слева = −x, −y).
    val aLit = (p.x * -0.6f + p.y * -0.8f) > 0f
    val faceA = Path().apply { moveTo(base.x, base.y); lineTo(baseA.x, baseA.y); lineTo(shA.x, shA.y); lineTo(tip.x, tip.y); close() }
    val faceB = Path().apply { moveTo(base.x, base.y); lineTo(tip.x, tip.y); lineTo(shB.x, shB.y); lineTo(baseB.x, baseB.y); close() }
    val lit = Brush.linearGradient(listOf(m.hi, m.mid, m.low), start = base, end = tip)
    val shade = Brush.linearGradient(listOf(m.mid, m.low, m.edge), start = base, end = tip)
    drawPath(faceA, if (aLit) lit else shade)
    drawPath(faceB, if (aLit) shade else lit)
    val outline = Path().apply {
        moveTo(baseA.x, baseA.y); lineTo(shA.x, shA.y); lineTo(tip.x, tip.y)
        lineTo(shB.x, shB.y); lineTo(baseB.x, baseB.y); close()
    }
    drawPath(outline, m.edge, style = Stroke(width = max(1f, l.r * 0.005f)))
    // Световое ребро по оси клинка — тонкая линия, гаснущая к острию.
    drawLine(
        brush = Brush.linearGradient(listOf(m.hi, m.hi.copy(alpha = 0f)), start = base, end = tip),
        start = base,
        end = tip - d * (0.02f * l.r),
        strokeWidth = max(1f, l.r * 0.006f),
        cap = StrokeCap.Round,
    )
}

/** Изумруд в золотой оправе на плече осевого клинка (ромб вдоль клинка). */
private fun DrawScope.drawEmerald(center: Offset, d: Offset, along: Float, across: Float, m: Metal, level: Float) {
    val p = Offset(-d.y, d.x)
    fun rhomb(a: Float, b: Float) = Path().apply {
        val q1 = center + d * a
        val q2 = center + p * b
        val q3 = center - d * a
        val q4 = center - p * b
        moveTo(q1.x, q1.y); lineTo(q2.x, q2.y); lineTo(q3.x, q3.y); lineTo(q4.x, q4.y); close()
    }
    drawPath(rhomb(along * 1.32f, across * 1.32f), Brush.linearGradient(listOf(m.hi, m.mid, m.low), center - p * across - d * along, center + p * across + d * along))
    drawPath(rhomb(along * 1.32f, across * 1.32f), m.edge, style = Stroke(width = max(1f, across * 0.12f)))
    val stone = rhomb(along, across)
    drawPath(
        stone,
        Brush.linearGradient(
            listOf(lerp(EmeraldDeadLight, EmeraldLight, level), lerp(EmeraldDeadBase, EmeraldBase, level), lerp(EmeraldDeadDark, EmeraldDark, level)),
            start = center - p * across - d * along,
            end = center + p * across + d * along,
        ),
    )
    if (level > 0.001f) {
        // Блик на камне — неподвижный, как отражение на огранке; не светится и не мигает.
        val spot = center - p * (across * 0.32f) - d * (along * 0.30f)
        drawCircle(Color.White.copy(alpha = 0.65f * level), radius = across * 0.22f, center = spot)
    }
}

private fun DrawScope.drawAxisBlades(l: OrnamentLayout, level: Float) {
    val m = metal(level)
    val r = l.r
    val start = l.guideStart
    for ((deg, end) in listOf(0f to l.guideEnd, 180f to l.guideEnd, 270f to l.topSpikeEnd, 90f to l.bottomSpikeEnd)) {
        val d = dir(deg)
        drawBlade(l, d, start, end, baseHalf = 0.030f * r, shoulderAt = 0.075f * r, shoulderHalf = 0.064f * r, m = m)
        if (end - start > 0.15f * r) {
            drawEmerald(Offset(l.cx, l.cy) + d * (start + 0.075f * r), d, along = 0.034f * r, across = 0.024f * r, m = m, level = level)
        }
    }
}

private fun DrawScope.drawDiagonalBlades(l: OrnamentLayout, level: Float) {
    val m = metal(level)
    val r = l.r
    for (deg in floatArrayOf(45f, 135f, 225f, 315f)) {
        drawBlade(l, dir(deg), l.guideStart + 0.01f * r, l.diagonalEnd, baseHalf = 0.012f * r, shoulderAt = 0.05f * r, shoulderHalf = 0.030f * r, m = m)
    }
}

/** Бусинный пояс сразу за кольцом — между клинками, как зернь на ювелирной оправе. */
private fun DrawScope.drawBeads(l: OrnamentLayout, level: Float) {
    val r = l.r
    val radius = (ORNAMENT_CLEAR + 0.03f) * r
    val bead = max(1.2f, 0.0085f * r)
    val m = metal(level)
    var deg = 0f
    while (deg < 360f) {
        val fromAxis = abs(((deg + 45f) % 90f) - 45f)
        if (fromAxis > 7f && abs(fromAxis - 45f) > 5f) {
            val c = Offset(l.cx, l.cy) + dir(deg) * radius
            drawCircle(m.low, radius = bead * 1.15f, center = c)
            drawCircle(m.mid, radius = bead, center = c)
            drawCircle(m.hi, radius = bead * 0.45f, center = c - Offset(bead * 0.3f, bead * 0.3f))
        }
        deg += 3.75f
    }
}

/**
 * Тонкие сужающиеся лучи: бронзовый след виден всегда едва-едва, золото при подключении
 * прорастает от кольца к кончику ([level] — доля длины).
 */
private fun DrawScope.drawRays(l: OrnamentLayout, level: Float) {
    val r = l.r
    val half = max(0.5f, r * 0.0034f)
    for (ray in l.rays) {
        val a = Offset(ray.x0, ray.y0)
        val b = Offset(ray.x1, ray.y1)
        drawTaper(a, b, half, Brush.linearGradient(listOf(RayBronze.copy(alpha = 0.08f), RayBronze.copy(alpha = 0f)), a, b))
        if (level > 0.001f) {
            val lit = a + (b - a) * level
            val alpha = 0.14f + 0.30f * ray.weight
            drawTaper(a, lit, half, Brush.linearGradient(listOf(RayGold.copy(alpha = alpha), RayGold.copy(alpha = 0f)), a, lit))
        }
    }
}

/** Треугольный луч: полуширина [half] у основания, острие на конце. */
private fun DrawScope.drawTaper(a: Offset, b: Offset, half: Float, brush: Brush) {
    val v = b - a
    val len = v.getDistance()
    if (len < 1f) return
    val p = Offset(-v.y / len, v.x / len) * half
    val path = Path().apply {
        moveTo(a.x + p.x, a.y + p.y)
        lineTo(b.x, b.y)
        lineTo(a.x - p.x, a.y - p.y)
        close()
    }
    drawPath(path, brush)
}

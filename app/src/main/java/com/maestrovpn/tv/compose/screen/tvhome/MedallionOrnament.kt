package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.lerp
import kotlin.math.max

/**
 * Декоративный слой вокруг медальона (07.11.2026, по референсу владельца): тонкие золотые лучи
 * разной длины, горизонтальные бронзово-золотые направляющие с ромбами и изумрудами, верхний и
 * нижний наконечники. Рисуется кодом (векторно), растров нет.
 *
 * Слой лежит ПОД медальоном и начинается за диском и плашками камней ([ORNAMENT_CLEAR]):
 * медальон, буква «M», кольцо и четыре его камня не меняются.
 *
 * Два состояния по реальному статусу VPN ([active] = подключено и не «Подключение…»):
 *  - ACTIVE: светлое золото с бликами, яркие изумруды с внутренним светом, заметные лучи,
 *    мягкое дыхание свечения (2,4 с) и узкий ореол вдоль кольца;
 *  - INACTIVE: тёмная бронза, тёмные изумруды без блика, лучи почти не видны; ореола, свечения
 *    и пульсации нет вовсе (всё световое умножается на [level] = 0).
 * Декор не скрывается: меняются только цвет, яркость, насыщенность и прозрачность.
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
    val level by animateFloatAsState(
        targetValue = if (active) 1f else 0f,
        animationSpec = tween(durationMillis = 600, easing = FastOutSlowInEasing),
        label = "ornamentLevel",
    )
    val transition = rememberInfiniteTransition(label = "ornamentPulse")
    val pulse by transition.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "ornamentBreath",
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
        // Свет есть только в ACTIVE: при level = 0 и дыхание, и ореол, и свечение камней — ноль.
        val breath = 0.78f + 0.22f * pulse
        val glow = level * breath
        val metal = ornamentMetal(level)
        val emerald = ornamentEmerald(level)
        clipRect(layout.clipLeft, layout.clipTop, layout.clipRight, layout.clipBottom) {
            drawHalo(layout, glow)
            drawRays(layout, level, breath)
            drawGuide(layout, side = -1f, metal = metal, emerald = emerald, glow = glow, level = level)
            drawGuide(layout, side = 1f, metal = metal, emerald = emerald, glow = glow, level = level)
            drawSpike(layout, dir = -1f, metal = metal, emerald = emerald, glow = glow, level = level)
            drawSpike(layout, dir = 1f, metal = metal, emerald = emerald, glow = glow, level = level)
        }
    }
}

private class Metal(val hi: Color, val mid: Color, val low: Color, val edge: Color)
private class Emerald(val light: Color, val base: Color, val dark: Color)

// ACTIVE: старое полированное золото; INACTIVE: потухшая тёмная бронза.
private val GoldHi = Color(0xFFFFE6A0)
private val GoldMid = Color(0xFFD3A447)
private val GoldLow = Color(0xFF8C601E)
private val GoldEdge = Color(0xFF4A3010)
private val BronzeHi = Color(0xFF5B4630)
private val BronzeMid = Color(0xFF443321)
private val BronzeLow = Color(0xFF2E2216)
private val BronzeEdge = Color(0xFF1C140B)

private val EmeraldLight = Color(0xFF8CFFC0)
private val EmeraldBase = Color(0xFF17C25A)
private val EmeraldDark = Color(0xFF06572A)
private val EmeraldDeadLight = Color(0xFF123522)
private val EmeraldDeadBase = Color(0xFF0B2516)
private val EmeraldDeadDark = Color(0xFF05140C)
private val EmeraldGlow = Color(0xFF2EF27A)
private val RayGold = Color(0xFFFFD877)
private val RayBronze = Color(0xFF6B5434)

private fun ornamentMetal(t: Float) = Metal(
    hi = lerp(BronzeHi, GoldHi, t),
    mid = lerp(BronzeMid, GoldMid, t),
    low = lerp(BronzeLow, GoldLow, t),
    edge = lerp(BronzeEdge, GoldEdge, t),
)

private fun ornamentEmerald(t: Float) = Emerald(
    light = lerp(EmeraldDeadLight, EmeraldLight, t),
    base = lerp(EmeraldDeadBase, EmeraldBase, t),
    dark = lerp(EmeraldDeadDark, EmeraldDark, t),
)

/**
 * Узкий ореол сразу за кольцом — только ACTIVE (glow = 0 в INACTIVE). Радиус ограничен ближайшей
 * границей клипа: иначе над подписью статуса ореол обрезался бы прямой линией.
 */
private fun DrawScope.drawHalo(l: OrnamentLayout, glow: Float) {
    if (glow <= 0.001f) return
    val reach = minOf(1.12f * l.r, l.cy - l.clipTop, l.clipBottom - l.cy, l.clipRight - l.cx)
    val inner = 0.74f * l.r
    if (reach <= inner + 0.08f * l.r) return
    val peak = minOf(0.82f * l.r, (inner + reach) / 2f)
    drawCircle(
        brush = Brush.radialGradient(
            inner / reach to Color.Transparent,
            peak / reach to RayGold.copy(alpha = 0.15f * glow),
            1f to Color.Transparent,
            center = Offset(l.cx, l.cy),
            radius = reach,
        ),
        radius = reach,
        center = Offset(l.cx, l.cy),
    )
}

private fun DrawScope.drawRays(l: OrnamentLayout, level: Float, breath: Float) {
    val width = max(1f, l.r * 0.0055f)
    val color = lerp(RayBronze, RayGold, level)
    for (ray in l.rays) {
        // INACTIVE: 0.045 — лучи «практически незаметны»; ACTIVE: 0.30..0.65 с дыханием.
        val activeAlpha = (0.30f + 0.35f * ray.weight) * breath
        val alpha = 0.045f + (activeAlpha - 0.045f) * level
        val start = Offset(ray.x0, ray.y0)
        val end = Offset(ray.x1, ray.y1)
        drawLine(
            brush = Brush.linearGradient(
                listOf(color.copy(alpha = alpha), color.copy(alpha = 0f)),
                start = start,
                end = end,
            ),
            start = start,
            end = end,
            strokeWidth = width,
            cap = StrokeCap.Round,
        )
    }
}

private fun metalBrushV(m: Metal, top: Float, bottom: Float) = Brush.verticalGradient(
    0f to m.edge, 0.22f to m.hi, 0.5f to m.mid, 0.82f to m.low, 1f to m.edge,
    startY = top, endY = bottom,
)

private fun metalBrushH(m: Metal, left: Float, right: Float) = Brush.horizontalGradient(
    0f to m.edge, 0.22f to m.hi, 0.5f to m.mid, 0.82f to m.low, 1f to m.edge,
    startX = left, endX = right,
)

private fun rhombPath(cx: Float, cy: Float, hw: Float, hh: Float) = Path().apply {
    moveTo(cx, cy - hh)
    lineTo(cx + hw, cy)
    lineTo(cx, cy + hh)
    lineTo(cx - hw, cy)
    close()
}

/** Ромб: золотая оправа, изумрудная вставка, блик и (только ACTIVE) внутренний свет. */
private fun DrawScope.drawJewel(
    cx: Float, cy: Float, hw: Float, hh: Float,
    metal: Metal, emerald: Emerald, glow: Float, level: Float,
) {
    if (glow > 0.001f) {
        val gr = max(hw, hh) * 1.8f
        drawCircle(
            brush = Brush.radialGradient(
                0f to EmeraldGlow.copy(alpha = 0.30f * glow),
                1f to Color.Transparent,
                center = Offset(cx, cy),
                radius = gr,
            ),
            radius = gr,
            center = Offset(cx, cy),
        )
    }
    val frame = rhombPath(cx, cy, hw, hh)
    drawPath(frame, metalBrushV(metal, cy - hh, cy + hh))
    drawPath(frame, metal.edge, style = Stroke(width = max(1f, hw * 0.06f)))
    val iw = hw * 0.58f
    val ih = hh * 0.58f
    val stone = rhombPath(cx, cy, iw, ih)
    drawPath(
        stone,
        Brush.linearGradient(
            listOf(emerald.light, emerald.base, emerald.dark),
            start = Offset(cx - iw, cy - ih),
            end = Offset(cx + iw, cy + ih),
        ),
    )
    drawPath(stone, metal.edge.copy(alpha = 0.8f), style = Stroke(width = max(1f, iw * 0.07f)))
    if (level > 0.001f) {
        // Блик на верхней левой грани камня и оправы — только в ACTIVE.
        drawPath(rhombPath(cx - iw * 0.28f, cy - ih * 0.30f, iw * 0.26f, ih * 0.22f), Color.White.copy(alpha = 0.55f * level))
        drawLine(
            Color.White.copy(alpha = 0.45f * level),
            Offset(cx - hw * 0.92f, cy - hh * 0.04f),
            Offset(cx - hw * 0.08f, cy - hh * 0.88f),
            strokeWidth = max(1f, hw * 0.05f),
            cap = StrokeCap.Round,
        )
    }
}

/** Горизонтальная направляющая: тонкие нити, клиновидная планка, муфта, большой и малый ромбы. */
private fun DrawScope.drawGuide(l: OrnamentLayout, side: Float, metal: Metal, emerald: Emerald, glow: Float, level: Float) {
    val r = l.r
    val u0 = l.guideStart
    val uEnd = l.guideEnd
    if (uEnd - u0 < 0.18f * r) return
    fun x(u: Float) = l.cx + side * u
    val cy = l.cy

    // Нити над и под планкой.
    val hair = max(1f, r * 0.008f)
    for (dy in floatArrayOf(-0.052f * r, 0.052f * r)) {
        drawLine(
            brush = Brush.horizontalGradient(
                listOf(metal.mid, metal.mid.copy(alpha = 0f)),
                startX = x(u0), endX = x(u0 + (uEnd - u0) * 0.9f),
            ),
            start = Offset(x(u0 + 0.02f * r), cy + dy),
            end = Offset(x(u0 + (uEnd - u0) * 0.9f), cy + dy),
            strokeWidth = hair,
            cap = StrokeCap.Round,
        )
    }

    // Клиновидная планка: толще у медальона, остриё у края.
    val t0 = 0.030f * r
    val t1 = 0.020f * r
    val ub = (u0 + 0.12f * r).coerceAtMost(uEnd - 0.14f * r).coerceAtLeast(u0 + 0.09f * r)
    val bar = Path().apply {
        moveTo(x(u0), cy - t0)
        lineTo(x(ub), cy - t1)
        lineTo(x(uEnd), cy)
        lineTo(x(ub), cy + t1)
        lineTo(x(u0), cy + t0)
        close()
    }
    drawPath(bar, metalBrushV(metal, cy - t0, cy + t0))
    drawPath(bar, metal.edge, style = Stroke(width = max(1f, r * 0.004f)))

    // Муфта у кольца.
    val ch = 0.048f * r
    val cw = 0.030f * r
    val cl = minOf(x(u0), x(u0 + cw))
    drawRect(metalBrushV(metal, cy - ch, cy + ch), topLeft = Offset(cl, cy - ch), size = Size(cw, ch * 2f))

    // Большой ромб с изумрудом и малый — ближе к краю.
    drawJewel(x(ub), cy, 0.072f * r, 0.085f * r, metal, emerald, glow, level)
    val us = ub + (uEnd - ub) * 0.55f
    if (uEnd - ub > 0.16f * r) drawJewel(x(us), cy, 0.040f * r, 0.048f * r, metal, emerald, glow, level)
}

/** Вертикальный наконечник: муфта, ромб с изумрудом, боковые шипы и клинок к острию. */
private fun DrawScope.drawSpike(l: OrnamentLayout, dir: Float, metal: Metal, emerald: Emerald, glow: Float, level: Float) {
    val r = l.r
    val v0 = l.spikeStart
    val vEnd = if (dir < 0f) l.topSpikeEnd else l.bottomSpikeEnd
    if (vEnd - v0 < 0.06f * r) return // места нет (низкий экран) — наконечник не рисуем
    fun y(v: Float) = l.cy + dir * v
    val cx = l.cx

    val cw = 0.068f * r
    val ch = 0.026f * r
    val ct = minOf(y(v0), y(v0 + ch))
    drawRect(metalBrushH(metal, cx - cw, cx + cw), topLeft = Offset(cx - cw, ct), size = Size(cw * 2f, ch))

    val jv = v0 + ch + 0.050f * r
    val jh = 0.050f * r
    val bladeStart = jv + jh
    if (vEnd > bladeStart + 0.04f * r) {
        val bw = 0.030f * r
        val blade = Path().apply {
            moveTo(cx - bw, y(bladeStart - 0.01f * r))
            lineTo(cx, y(vEnd))
            lineTo(cx + bw, y(bladeStart - 0.01f * r))
            close()
        }
        drawPath(blade, metalBrushH(metal, cx - bw, cx + bw))
        drawPath(blade, metal.edge, style = Stroke(width = max(1f, r * 0.004f)))
        // Боковые шипы у основания клинка — зеркально.
        for (s in floatArrayOf(-1f, 1f)) {
            val spur = Path().apply {
                moveTo(cx + s * 0.020f * r, y(bladeStart))
                lineTo(cx + s * 0.090f * r, y(bladeStart + 0.055f * r))
                lineTo(cx + s * 0.030f * r, y(bladeStart + 0.030f * r))
                close()
            }
            drawPath(spur, metal.mid)
            drawPath(spur, metal.edge, style = Stroke(width = max(1f, r * 0.003f)))
        }
    }
    if (vEnd >= jv + jh) drawJewel(cx, y(jv), 0.042f * r, jh, metal, emerald, glow, level)
}

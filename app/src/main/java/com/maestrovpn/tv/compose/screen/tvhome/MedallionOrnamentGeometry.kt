package com.maestrovpn.tv.compose.screen.tvhome

import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.min
import kotlin.math.sin

/**
 * Геометрия декоративного слоя вокруг медальона (07.11.2026): лучи, горизонтальные направляющие
 * с ромбами и верхний/нижний наконечники. Чистые числа без Compose — проверяются unit-тестом
 * ([MedallionOrnamentGeometryTest]): симметрия, ничего не заходит на диск и камни, ничего не
 * выходит за бокс героя и не наезжает на подпись статуса.
 *
 * Все длины — в долях r = половины бокса медальона (бокс = CARVED_MEDALLION_DIAMETER героя).
 * Диск с золотой полосой кончается на 372/512 r, плашки камней в состоянии «off» — на 402/512 r
 * (= 0.785 r), поэтому декор начинается с [ORNAMENT_CLEAR] и не трогает ни диск, ни камни.
 */
internal const val ORNAMENT_CLEAR = 0.81f
private const val GUIDE_REACH = 1.26f
private const val SPIKE_REACH = 0.99f
private const val RAY_START = ORNAMENT_CLEAR + 0.02f
private const val RAY_REACH = 0.62f
private const val RAY_MIN_LEN = 0.05f
internal const val RAY_COUNT = 96
private const val RAY_AXIS_GAP_DEG = 10f

/**
 * Длины лучей на 45° (12 лучей по 3,75°). Узор зеркальный: p[i] == p[(12 - i) % 12], поэтому
 * картина симметрична и по вертикали, и по горизонтали. Самые длинные — по диагоналям.
 */
private val RAY_PATTERN = floatArrayOf(1f, 0.42f, 0.7f, 0.5f, 0.85f, 0.46f, 1f, 0.46f, 0.85f, 0.5f, 0.7f, 0.42f)

internal data class OrnamentRay(
    val x0: Float, val y0: Float, val x1: Float, val y1: Float,
    /** 0..1 — относительная длина по узору, от неё зависит яркость. */
    val weight: Float,
)

internal data class OrnamentLayout(
    val cx: Float,
    val cy: Float,
    val r: Float,
    val clipLeft: Float,
    val clipTop: Float,
    val clipRight: Float,
    val clipBottom: Float,
    /** Расстояние от центра до начала и конца каждой направляющей (одинаково слева и справа). */
    val guideStart: Float,
    val guideEnd: Float,
    val spikeStart: Float,
    /** Конец верхнего / нижнего наконечника от центра; меньше [spikeStart] — наконечника нет. */
    val topSpikeEnd: Float,
    val bottomSpikeEnd: Float,
    val rays: List<OrnamentRay>,
)

/**
 * @param width/height размер бокса героя (кнопка медальона) в px.
 * @param medallionLeft/medallionTop/medallionSize — тот же бокс медальона, что у MonogramMedallion.
 * @param bottomLimit ниже этой линии лежит подпись статуса: декор туда не заходит.
 * @param margin отступ от всех границ в px.
 */
internal fun medallionOrnamentLayout(
    width: Float,
    height: Float,
    medallionLeft: Float,
    medallionTop: Float,
    medallionSize: Float,
    bottomLimit: Float,
    margin: Float,
): OrnamentLayout {
    val r = medallionSize / 2f
    val cx = medallionLeft + r
    val cy = medallionTop + r
    // Обрезка по боксу героя и над подписью; по горизонтали — симметрично относительно центра
    // медальона (он смещён на 1/712 ширины от центра бокса), иначе направляющие разной длины.
    val half = min(cx, width - cx)
    val clipLeft = cx - half
    val clipRight = cx + half
    // По вертикали тоже симметрично: снизу ограничивает подпись статуса, сверху — бокс героя;
    // берём меньшее, чтобы верхний и нижний наконечники и лучи были зеркальными.
    val vHalf = maxOf(0f, min(cy, min(height, bottomLimit) - cy))
    val clipTop = cy - vHalf
    val clipBottom = cy + vHalf

    val guideStart = ORNAMENT_CLEAR * r
    val guideEnd = min(GUIDE_REACH * r, half - margin)
    val spikeStart = ORNAMENT_CLEAR * r
    val topSpikeEnd = min(SPIKE_REACH * r, vHalf - margin)
    val bottomSpikeEnd = topSpikeEnd

    val rays = ArrayList<OrnamentRay>(RAY_COUNT)
    val step = 360.0 / RAY_COUNT
    for (k in 0 until RAY_COUNT) {
        val deg = k * step
        val fromAxis = abs(((deg + 45.0) % 90.0) - 45.0)
        if (fromAxis < RAY_AXIS_GAP_DEG) continue // на осях — направляющие и наконечники
        val p = RAY_PATTERN[k % RAY_PATTERN.size]
        val a = deg * PI / 180.0
        val dx = cos(a).toFloat()
        val dy = sin(a).toFloat()
        val tx = when {
            dx > 1e-6f -> (clipRight - cx) / dx
            dx < -1e-6f -> (clipLeft - cx) / dx
            else -> Float.MAX_VALUE
        }
        val ty = when {
            dy > 1e-6f -> (clipBottom - cy) / dy
            dy < -1e-6f -> (clipTop - cy) / dy
            else -> Float.MAX_VALUE
        }
        val start = RAY_START * r
        val end = min((RAY_START + RAY_REACH * p) * r, min(tx, ty) - margin)
        if (end - start < RAY_MIN_LEN * r) continue
        rays += OrnamentRay(cx + dx * start, cy + dy * start, cx + dx * end, cy + dy * end, p)
    }

    return OrnamentLayout(
        cx = cx, cy = cy, r = r,
        clipLeft = clipLeft, clipTop = clipTop, clipRight = clipRight, clipBottom = clipBottom,
        guideStart = guideStart, guideEnd = guideEnd,
        spikeStart = spikeStart, topSpikeEnd = topSpikeEnd, bottomSpikeEnd = bottomSpikeEnd,
        rays = rays,
    )
}

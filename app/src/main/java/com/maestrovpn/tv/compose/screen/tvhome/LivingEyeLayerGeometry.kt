package com.maestrovpn.tv.compose.screen.tvhome

internal data class LivingEyeLayerBounds(
    val left: Float,
    val top: Float,
    val right: Float,
    val bottom: Float,
    val scale: Float,
) {
    val centerX: Float get() = (left + right) / 2f
    val centerY: Float get() = (top + bottom) / 2f
}

internal data class LivingEyeLayerFit(
    val stateBounds: LivingEyeLayerBounds,
    val scale: Float,
    val translationX: Float,
    val translationY: Float,
) {
    fun mapSourceX(x: Float): Float = x * scale + translationX

    fun mapSourceY(y: Float): Float = y * scale + translationY

    fun mapSourceLength(length: Float): Float = length * scale

    fun mapSourceLengthX(length: Float): Float = length * scale

    fun mapSourceLengthY(length: Float): Float = length * scale

    fun mapSourceBounds(
        left: Float,
        top: Float,
        right: Float,
        bottom: Float,
    ): LivingEyeLayerBounds = LivingEyeLayerBounds(
        left = mapSourceX(left),
        top = mapSourceY(top),
        right = mapSourceX(right),
        bottom = mapSourceY(bottom),
        scale = scale,
    )
}

internal data class LivingEyeLayerPoint(val x: Float, val y: Float)

internal data class LivingEyeApertureContour(
    val upper: List<LivingEyeLayerPoint>,
    val lower: List<LivingEyeLayerPoint>,
) {
    val bounds: LivingEyeLayerBounds
        get() = LivingEyeLayerBounds(
            left = minOf(upper.minOf { it.x }, lower.minOf { it.x }),
            top = minOf(upper.minOf { it.y }, lower.minOf { it.y }),
            right = maxOf(upper.maxOf { it.x }, lower.maxOf { it.x }),
            bottom = maxOf(upper.maxOf { it.y }, lower.maxOf { it.y }),
            scale = 1f,
        )
}


internal fun livingEyeApertureContour(
    layerFit: LivingEyeLayerFit,
    closure: Float,
    seamOverlapPx: Float,
): LivingEyeApertureContour {
    val phase = closure.coerceIn(0f, 1f)
    val overlap = seamOverlapPx.coerceAtLeast(0f)
    val sourceXs = (
        LIVING_EYE_APERTURE_UPPER_SOURCE.map { it.x } +
            LIVING_EYE_APERTURE_LOWER_SOURCE.map { it.x }
        ).distinct().sorted()

    fun sourceGeometryAt(x: Float): Triple<Float, Float, Float> {
        val upperY = livingEyeSourceYAtX(LIVING_EYE_APERTURE_UPPER_SOURCE, x)
        val lowerY = livingEyeSourceYAtX(LIVING_EYE_APERTURE_LOWER_SOURCE, x)
        val seamY = upperY + (lowerY - upperY) * LIVING_EYE_UPPER_LID_TRAVEL_SHARE
        return Triple(upperY, lowerY, seamY)
    }

    fun mappedPoint(x: Float, upper: Boolean): LivingEyeLayerPoint {
        val (upperY, lowerY, seamSourceY) = sourceGeometryAt(x)
        val sourceY = if (upper) upperY else lowerY
        val closingSourceY = sourceY + (seamSourceY - sourceY) * phase
        val seamY = layerFit.mapSourceY(seamSourceY)
        val closingY = layerFit.mapSourceY(closingSourceY)
        val contractedY = if (upper) {
            minOf(seamY, closingY + overlap)
        } else {
            maxOf(seamY, closingY - overlap)
        }
        return LivingEyeLayerPoint(
            x = layerFit.mapSourceX(x),
            y = contractedY,
        )
    }

    return LivingEyeApertureContour(
        upper = sourceXs.map { mappedPoint(it, upper = true) },
        lower = sourceXs.map { mappedPoint(it, upper = false) },
    )
}


private fun livingEyeSourceYAtX(points: List<LivingEyeLayerPoint>, x: Float): Float {
    if (x <= points.first().x) return points.first().y
    if (x >= points.last().x) return points.last().y
    val rightIndex = points.indexOfFirst { it.x >= x }
    val left = points[rightIndex - 1]
    val right = points[rightIndex]
    val fraction = (x - left.x) / (right.x - left.x)
    return left.y + (right.y - left.y) * fraction
}

/**
 * Fits the complete emerald eye layer inside the fixed bronze medallion.
 *
 * The actual offset state rectangle is mapped from the owner-frame coordinates,
 * then its full horizontal extent is fitted into the bronze inset. One uniform
 * scale and translation maps every blink state, the aperture, iris, pupil and
 * catchlight so their source registration cannot drift.
 */
internal fun fitLivingEyeLayer(width: Float, height: Float): LivingEyeLayerFit {
    val medallionSize = minOf(width, height)
    val canvasLeft = (width - medallionSize) / 2f
    val canvasTop = (height - medallionSize) / 2f
    val virtualScale = medallionSize / LIVING_EYE_VIRTUAL_SIZE
    val rawStateLeft =
        canvasLeft + (LIVING_EYE_STATE_X - LIVING_EYE_VIRTUAL_ORIGIN_X) * virtualScale
    val rawStateTop =
        canvasTop + (LIVING_EYE_STATE_Y - LIVING_EYE_VIRTUAL_ORIGIN_Y) * virtualScale
    val rawStateRight =
        canvasLeft +
            (LIVING_EYE_STATE_X + LIVING_EYE_STATE_WIDTH - LIVING_EYE_VIRTUAL_ORIGIN_X) *
            virtualScale
    val rawStateBottom =
        canvasTop +
            (LIVING_EYE_STATE_Y + LIVING_EYE_STATE_HEIGHT - LIVING_EYE_VIRTUAL_ORIGIN_Y) *
            virtualScale

    // Apply the owner-approved anatomy scale and offsets exactly once. Every anatomy consumer uses
    // this fit, so blink, gaze, pupil, catchlight and the contact seam remain registered.
    val layerScale = LIVING_EYE_ANATOMY_SCALE
    val anatomyOffsetX = medallionSize * LIVING_EYE_OFFSET_X_FRACTION
    val anatomyOffsetY = medallionSize * LIVING_EYE_OFFSET_Y_FRACTION
    val layerTranslationX =
        width / 2f - (rawStateLeft + rawStateRight) / 2f * layerScale + anatomyOffsetX
    val layerTranslationY =
        height / 2f - (rawStateTop + rawStateBottom) / 2f * layerScale + anatomyOffsetY
    val scale = virtualScale * layerScale
    val translationX =
        (canvasLeft - LIVING_EYE_VIRTUAL_ORIGIN_X * virtualScale) * layerScale +
            layerTranslationX
    val translationY =
        (canvasTop - LIVING_EYE_VIRTUAL_ORIGIN_Y * virtualScale) * layerScale +
            layerTranslationY

    return LivingEyeLayerFit(
        stateBounds = LivingEyeLayerBounds(
            left = LIVING_EYE_STATE_X * scale + translationX,
            top = LIVING_EYE_STATE_Y * scale + translationY,
            right = (LIVING_EYE_STATE_X + LIVING_EYE_STATE_WIDTH) * scale + translationX,
            bottom = (LIVING_EYE_STATE_Y + LIVING_EYE_STATE_HEIGHT) * scale + translationY,
            scale = scale,
        ),
        scale = scale,
        translationX = translationX,
        translationY = translationY,
    )
}

/**
 * Отступ бронзового кольца от края медальона, в пикселях холста. Слой глаза подрезается по этому
 * кругу: лишняя зелень уходит ПОД кольцо вместо того, чтобы тащить за собой весь глаз вниз по
 * размеру. Единственный источник числа — [LIVING_EYE_BRONZE_INSET_FRACTION].
 */
internal fun livingEyeBronzeInset(width: Float, height: Float): Float =
    minOf(width, height) * LIVING_EYE_BRONZE_INSET_FRACTION

/**
 * Pure, size-relative profile for the thin aperture contact seam. The baked bronze and eye-surround
 * material stays in the single registered ring art.
 */
internal data class LivingEyeIntegrationProfile(
    val layerFit: LivingEyeLayerFit,
    val contactSeamWidthPx: Float,
    val contactSeamAlpha: Float,
) {
    val fitScale: Float get() = layerFit.scale
    val stateBounds: LivingEyeLayerBounds get() = layerFit.stateBounds
}

internal fun livingEyeIntegrationProfile(
    width: Float,
    height: Float,
): LivingEyeIntegrationProfile {
    val medallionSize = minOf(width, height)
    return LivingEyeIntegrationProfile(
        layerFit = fitLivingEyeLayer(width, height),
        contactSeamWidthPx = medallionSize * LIVING_EYE_CONTACT_SHADOW_FRACTION,
        contactSeamAlpha = 0.18f,
    )
}

internal data class LivingEyeRenderPolicy(
    val eyeLayersEnabled: Boolean,
    val glowEnabled: Boolean,
)

/** Full closure exposes baked eye-surround material and the thin contact seam. */
internal fun livingEyeRenderPolicy(closure: Float): LivingEyeRenderPolicy {
    val livingLayersEnabled =
        closure.coerceIn(0f, 1f) < LIVING_EYE_FULLY_CLOSED_PHASE
    return LivingEyeRenderPolicy(
        eyeLayersEnabled = livingLayersEnabled,
        glowEnabled = livingLayersEnabled,
    )
}

internal const val LIVING_EYE_STATE_X = 230f
internal const val LIVING_EYE_STATE_Y = 745f
internal const val LIVING_EYE_STATE_WIDTH = 890f
internal const val LIVING_EYE_STATE_HEIGHT = 635f

private const val LIVING_EYE_VIRTUAL_ORIGIN_X = 268.8f
private const val LIVING_EYE_VIRTUAL_ORIGIN_Y = 637.3f
private const val LIVING_EYE_VIRTUAL_SIZE = 822.5f
internal const val LIVING_EYE_ANATOMY_SCALE = 1.10f
internal const val LIVING_EYE_OFFSET_X_FRACTION = 3.5f / 238f
internal const val LIVING_EYE_OFFSET_Y_FRACTION = 7f / 238f
private const val LIVING_EYE_BRONZE_INSET_FRACTION = 26f / 520f
private const val LIVING_EYE_CONTACT_SHADOW_FRACTION = 3f / 520f
private const val LIVING_EYE_UPPER_LID_TRAVEL_SHARE = 0.70f
private const val LIVING_EYE_FULLY_CLOSED_PHASE = 0.999f

private val LIVING_EYE_APERTURE_UPPER_SOURCE = listOf(
    LivingEyeLayerPoint(388f, 1083f),
    LivingEyeLayerPoint(405f, 1061f),
    LivingEyeLayerPoint(430f, 1037f),
    LivingEyeLayerPoint(460f, 1014f),
    LivingEyeLayerPoint(500f, 993f),
    LivingEyeLayerPoint(540f, 978f),
    LivingEyeLayerPoint(580f, 968f),
    LivingEyeLayerPoint(620f, 961f),
    LivingEyeLayerPoint(660f, 957f),
    LivingEyeLayerPoint(700f, 957f),
    LivingEyeLayerPoint(740f, 962f),
    LivingEyeLayerPoint(780f, 973f),
    LivingEyeLayerPoint(820f, 990f),
    LivingEyeLayerPoint(860f, 1011f),
    LivingEyeLayerPoint(900f, 1036f),
    LivingEyeLayerPoint(932f, 1061f),
    LivingEyeLayerPoint(957f, 1083f),
)

private val LIVING_EYE_APERTURE_LOWER_SOURCE = listOf(
    LivingEyeLayerPoint(388f, 1083f),
    LivingEyeLayerPoint(420f, 1104f),
    LivingEyeLayerPoint(460f, 1123f),
    LivingEyeLayerPoint(500f, 1139f),
    LivingEyeLayerPoint(540f, 1152f),
    LivingEyeLayerPoint(580f, 1162f),
    LivingEyeLayerPoint(620f, 1170f),
    LivingEyeLayerPoint(660f, 1174f),
    LivingEyeLayerPoint(700f, 1172f),
    LivingEyeLayerPoint(740f, 1167f),
    LivingEyeLayerPoint(780f, 1159f),
    LivingEyeLayerPoint(820f, 1148f),
    LivingEyeLayerPoint(860f, 1133f),
    LivingEyeLayerPoint(900f, 1115f),
    LivingEyeLayerPoint(932f, 1098f),
    LivingEyeLayerPoint(957f, 1083f),
)

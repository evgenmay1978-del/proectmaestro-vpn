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

internal data class LivingEyeLash(
    val root: LivingEyeLayerPoint,
    val control1: LivingEyeLayerPoint,
    val control2: LivingEyeLayerPoint,
    val tip: LivingEyeLayerPoint,
    val width: Float,
    val alpha: Float,
    val upper: Boolean,
)

/** Fixed follicles follow the actual lid margin, never gaze or a separately fitted eye. */
internal fun livingEyeLashes(
    layerFit: LivingEyeLayerFit,
    closure: Float,
): List<LivingEyeLash> {
    val phase = closure.coerceIn(0f, 1f)
    val contour = livingEyeApertureContour(layerFit, phase, seamOverlapPx = 0f)
    fun lashes(specs: List<LivingEyeLashSpec>, upper: Boolean): List<LivingEyeLash> {
        val lid = if (upper) contour.upper else contour.lower
        return specs.map { spec ->
            val x = lid.first().x + (lid.last().x - lid.first().x) * spec.fraction
            val root = LivingEyeLayerPoint(x, livingEyeSourceYAtX(lid, x))
            val length = layerFit.mapSourceLength(spec.length)
            val fan = (spec.fraction - 0.46f) * 1.85f
            val dx = length * (fan + spec.sweep * 0.45f)
            val curl = length * spec.sweep * 1.2f
            // Upper lashes roll down as that lid closes; the shorter lower row stays below it.
            val dy = length * if (upper) -1f + 1.65f * phase else 1f
            LivingEyeLash(
                root = root,
                control1 = LivingEyeLayerPoint(x + dx * 0.38f - curl * 0.25f, root.y + dy * 0.25f),
                control2 = LivingEyeLayerPoint(x + dx * 0.78f + curl, root.y + dy * 0.88f),
                tip = LivingEyeLayerPoint(x + dx + curl * 0.20f, root.y + dy * 0.68f),
                width = layerFit.mapSourceLength(spec.width * 0.84f),
                alpha = spec.alpha * if (upper) 1f else 1f - 0.7f * phase,
                upper = upper,
            )
        }
    }
    return lashes(LIVING_EYE_UPPER_LASHES, upper = true) +
        lashes(LIVING_EYE_LOWER_LASHES, upper = false)
}

private data class LivingEyeLashSpec(
    val fraction: Float,
    val length: Float,
    val sweep: Float,
    val width: Float,
    val alpha: Float,
)

// Authored irregular follicles, not random per-frame noise or an evenly spaced spoke pattern.
// Small, irregular pairs use different sweeps and lengths. Fan direction begins at the root,
// with an independent curl along the shaft, rather than a row of vertical stalks with bent tips.
private val LIVING_EYE_UPPER_LASHES = listOf(
    LivingEyeLashSpec(0.082f, 23f, -0.27f, 3.2f, 0.78f),
    LivingEyeLashSpec(0.125f, 32f, -0.13f, 3.9f, 0.89f),
    LivingEyeLashSpec(0.136f, 27f, -0.31f, 3.3f, 0.84f),
    LivingEyeLashSpec(0.188f, 41f, -0.05f, 4.2f, 0.94f),
    LivingEyeLashSpec(0.225f, 48f, 0.12f, 3.9f, 0.92f),
    LivingEyeLashSpec(0.234f, 34f, -0.22f, 3.3f, 0.85f),
    LivingEyeLashSpec(0.284f, 51f, -0.04f, 4.1f, 0.95f),
    LivingEyeLashSpec(0.312f, 42f, 0.20f, 3.5f, 0.87f),
    LivingEyeLashSpec(0.320f, 55f, -0.17f, 4.2f, 0.96f),
    LivingEyeLashSpec(0.363f, 45f, 0.10f, 3.7f, 0.90f),
    LivingEyeLashSpec(0.373f, 59f, -0.10f, 4.1f, 0.93f),
    LivingEyeLashSpec(0.417f, 46f, 0.25f, 3.3f, 0.84f),
    LivingEyeLashSpec(0.449f, 63f, -0.18f, 4.4f, 0.97f),
    LivingEyeLashSpec(0.456f, 52f, 0.12f, 3.9f, 0.91f),
    LivingEyeLashSpec(0.495f, 60f, -0.27f, 3.5f, 0.88f),
    LivingEyeLashSpec(0.542f, 49f, 0.20f, 4.1f, 0.93f),
    LivingEyeLashSpec(0.550f, 64f, -0.07f, 4.2f, 0.96f),
    LivingEyeLashSpec(0.590f, 52f, 0.29f, 3.5f, 0.86f),
    LivingEyeLashSpec(0.633f, 67f, 0.04f, 4.2f, 0.95f),
    LivingEyeLashSpec(0.641f, 55f, -0.20f, 3.7f, 0.90f),
    LivingEyeLashSpec(0.680f, 63f, 0.18f, 4.1f, 0.94f),
    LivingEyeLashSpec(0.723f, 50f, -0.13f, 3.5f, 0.88f),
    LivingEyeLashSpec(0.731f, 60f, 0.27f, 4.2f, 0.96f),
    LivingEyeLashSpec(0.770f, 48f, 0.02f, 3.9f, 0.93f),
    LivingEyeLashSpec(0.810f, 57f, 0.32f, 4.1f, 0.95f),
    LivingEyeLashSpec(0.818f, 43f, -0.10f, 3.5f, 0.87f),
    LivingEyeLashSpec(0.859f, 50f, 0.23f, 3.9f, 0.92f),
    LivingEyeLashSpec(0.891f, 37f, 0.05f, 3.5f, 0.88f),
    LivingEyeLashSpec(0.900f, 33f, 0.29f, 3.3f, 0.83f),
)

private val LIVING_EYE_LOWER_LASHES = listOf(
    LivingEyeLashSpec(0.145f, 16f, -0.18f, 1.8f, 0.58f),
    LivingEyeLashSpec(0.207f, 21f, 0.12f, 2.1f, 0.68f),
    LivingEyeLashSpec(0.279f, 18f, -0.14f, 1.8f, 0.61f),
    LivingEyeLashSpec(0.334f, 26f, 0.20f, 2.3f, 0.72f),
    LivingEyeLashSpec(0.403f, 22f, -0.21f, 2.0f, 0.65f),
    LivingEyeLashSpec(0.469f, 28f, 0.06f, 2.3f, 0.71f),
    LivingEyeLashSpec(0.542f, 23f, -0.17f, 1.8f, 0.62f),
    LivingEyeLashSpec(0.598f, 30f, 0.23f, 2.3f, 0.73f),
    LivingEyeLashSpec(0.672f, 25f, -0.05f, 2.1f, 0.67f),
    LivingEyeLashSpec(0.728f, 31f, 0.27f, 2.3f, 0.72f),
    LivingEyeLashSpec(0.793f, 22f, 0.03f, 2.0f, 0.64f),
    LivingEyeLashSpec(0.859f, 20f, 0.18f, 1.8f, 0.59f),
)

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
            LIVING_EYE_APERTURE_LOWER_SOURCE.map { it.x } +
            LIVING_EYE_CLOSED_SEAM_SOURCE.map { it.x }
        ).distinct().sorted()

    fun sourceGeometryAt(x: Float): Triple<Float, Float, Float> {
        val upperY = livingEyeSourceYAtX(LIVING_EYE_APERTURE_UPPER_SOURCE, x)
        val lowerY = livingEyeSourceYAtX(LIVING_EYE_APERTURE_LOWER_SOURCE, x)
        val seamY = livingEyeSourceYAtX(LIVING_EYE_CLOSED_SEAM_SOURCE, x)
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
 * Applies one owner-approved uniform scale and translation to the complete living-eye anatomy.
 *
 * The mapped bounds may extend beyond the canvas; the bronze socket clip hides that overflow.
 * Every blink state, aperture, iris, pupil, catchlight, gaze offset and seam shares this fit so
 * source registration cannot drift.
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

/** The animated aperture reveals the registered eye-surround already drawn below this layer. */
internal fun livingEyeRenderPolicy(closure: Float): LivingEyeRenderPolicy {
    val phase = closure.coerceIn(0f, 1f)
    val livingLayersEnabled = phase < LIVING_EYE_FULLY_CLOSED_PHASE
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
private const val LIVING_EYE_FULLY_CLOSED_PHASE = 0.999f

// Registered to the unchanged green master: every state keeps the same two corners and closes
// onto its existing fold, rather than introducing a second, independently shaped eyelid.
private val LIVING_EYE_APERTURE_UPPER_SOURCE = listOf(
    LivingEyeLayerPoint(312.889f, 1045.174f),
    LivingEyeLayerPoint(356.632f, 1024.760f),
    LivingEyeLayerPoint(414.957f, 995.015f),
    LivingEyeLayerPoint(473.282f, 967.602f),
    LivingEyeLayerPoint(531.607f, 948.355f),
    LivingEyeLayerPoint(589.931f, 937.856f),
    LivingEyeLayerPoint(664.004f, 933.774f),
    LivingEyeLayerPoint(735.744f, 939.606f),
    LivingEyeLayerPoint(794.068f, 954.187f),
    LivingEyeLayerPoint(852.393f, 974.018f),
    LivingEyeLayerPoint(910.718f, 997.931f),
    LivingEyeLayerPoint(969.043f, 1025.344f),
    LivingEyeLayerPoint(1015.119f, 1045.174f),
)

private val LIVING_EYE_APERTURE_LOWER_SOURCE = listOf(
    LivingEyeLayerPoint(312.889f, 1045.174f),
    LivingEyeLayerPoint(356.632f, 1060.338f),
    LivingEyeLayerPoint(414.957f, 1086.001f),
    LivingEyeLayerPoint(473.282f, 1109.331f),
    LivingEyeLayerPoint(531.607f, 1127.412f),
    LivingEyeLayerPoint(589.931f, 1140.243f),
    LivingEyeLayerPoint(664.004f, 1146.659f),
    LivingEyeLayerPoint(735.744f, 1141.410f),
    LivingEyeLayerPoint(794.068f, 1129.745f),
    LivingEyeLayerPoint(852.393f, 1111.664f),
    LivingEyeLayerPoint(910.718f, 1088.334f),
    LivingEyeLayerPoint(969.043f, 1063.255f),
    LivingEyeLayerPoint(1015.119f, 1045.174f),
)

private val LIVING_EYE_CLOSED_SEAM_SOURCE = listOf(
    LivingEyeLayerPoint(312.889f, 1045.174f),
    LivingEyeLayerPoint(356.632f, 1059.755f),
    LivingEyeLayerPoint(414.957f, 1079.002f),
    LivingEyeLayerPoint(473.282f, 1093.584f),
    LivingEyeLayerPoint(531.607f, 1104.665f),
    LivingEyeLayerPoint(589.931f, 1109.915f),
    LivingEyeLayerPoint(664.004f, 1111.664f),
    LivingEyeLayerPoint(735.744f, 1106.415f),
    LivingEyeLayerPoint(794.068f, 1098.833f),
    LivingEyeLayerPoint(852.393f, 1087.168f),
    LivingEyeLayerPoint(910.718f, 1074.336f),
    LivingEyeLayerPoint(969.043f, 1058.589f),
    LivingEyeLayerPoint(1015.119f, 1045.174f),
)

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

    // ⛔ БОЛЬШЕ НЕ УЖИМАЕМ СЛОЙ. Здесь стояло
    //     layerScale = (medallionSize - inset * 2) / (rawStateRight - rawStateLeft)
    // — оно вписывало в бронзовый отступ ВЕСЬ слой целиком. Внешняя граница арта и есть зелень,
    // поэтому убрать её с кольца масштабированием можно было только утащив за собой радужку,
    // зрачок и блик: глаз терял 16.8% стороны и 30.8% площади (0.632219 → 0.525843), и владелец
    // 31.07.2026 это увидел — «глаз сам стал меньше, можно было оставить как был».
    // Зелень вылезала всего на 21.3 px с каждой стороны при канве 520 — это подрезается клипом по
    // кольцу (livingEyeBronzeInset + clipPath в LivingEyeMedallion), а не уменьшением всего глаза.
    val layerScale = 1f
    val layerTranslationX = width / 2f - (rawStateLeft + rawStateRight) / 2f * layerScale
    val layerTranslationY = height / 2f - (rawStateTop + rawStateBottom) / 2f * layerScale
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
 * Pure, size-relative profile for the two shadows that seat the living eye under the bronze.
 * The profile owns the already-approved fit so callers cannot introduce a second scale or a
 * second registration while adding visual integration.
 */
internal data class LivingEyeIntegrationProfile(
    val layerFit: LivingEyeLayerFit,
    val innerOcclusionWidthPx: Float,
    val innerOcclusionAlpha: Float,
    val eyelidContactShadowBlurPx: Float,
    val eyelidContactShadowAlpha: Float,
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
        innerOcclusionWidthPx = medallionSize * LIVING_EYE_INNER_OCCLUSION_FRACTION,
        innerOcclusionAlpha = 0.36f,
        eyelidContactShadowBlurPx = medallionSize * LIVING_EYE_CONTACT_SHADOW_FRACTION,
        eyelidContactShadowAlpha = 0.24f,
    )
}

internal const val LIVING_EYE_STATE_X = 230f
internal const val LIVING_EYE_STATE_Y = 745f
internal const val LIVING_EYE_STATE_WIDTH = 890f
internal const val LIVING_EYE_STATE_HEIGHT = 635f

private const val LIVING_EYE_VIRTUAL_ORIGIN_X = 268.8f
private const val LIVING_EYE_VIRTUAL_ORIGIN_Y = 637.3f
private const val LIVING_EYE_VIRTUAL_SIZE = 822.5f
private const val LIVING_EYE_BRONZE_INSET_FRACTION = 26f / 520f
private const val LIVING_EYE_INNER_OCCLUSION_FRACTION = 0.035f
private const val LIVING_EYE_CONTACT_SHADOW_FRACTION = 0.015f

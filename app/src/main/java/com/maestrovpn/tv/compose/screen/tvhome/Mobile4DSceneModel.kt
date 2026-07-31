package com.maestrovpn.tv.compose.screen.tvhome

import kotlin.math.abs

internal enum class Mobile4DLightSide {
    Left,
    Right,
}

internal data class Mobile4DLightMix(
    val activeSide: Mobile4DLightSide,
    val centerWeight: Float,
    val sideWeight: Float,
) {
    val leftWeight: Float
        get() = if (activeSide == Mobile4DLightSide.Left) sideWeight else 0f
    val rightWeight: Float
        get() = if (activeSide == Mobile4DLightSide.Right) sideWeight else 0f
}

internal data class Mobile4DSceneLayout(
    val scale: Float,
    val translationX: Float,
    val translationY: Float,
    val medallionCenterX: Float,
    val medallionCenterY: Float,
    val medallionRadiusX: Float,
    val medallionRadiusY: Float,
) {
    fun mapMasterX(x: Float): Float = x * scale + translationX

    fun mapMasterY(y: Float): Float = y * scale + translationY
}

internal enum class Mobile4DParallaxLayer(val maximumDepthDp: Float) {
    Wood(0.5f),
    Frame(1.5f),
    Cartouche(2.5f),
    Vines(3.5f),
    RingAndEye(5f),
}

internal data class Mobile4DParallaxOffset(
    val xDp: Float,
    val yDp: Float,
)

internal enum class Mobile4DEyeState {
    Disconnected,
    Connecting,
    Connected,
}

internal fun mobile4DSceneLayout(width: Float, height: Float): Mobile4DSceneLayout {
    val scale = maxOf(width / MOBILE_4D_MASTER_WIDTH, height / MOBILE_4D_MASTER_HEIGHT)
    val translationX = (width - MOBILE_4D_MASTER_WIDTH * scale) / 2f
    val translationY = (height - MOBILE_4D_MASTER_HEIGHT * scale) / 2f

    return Mobile4DSceneLayout(
        scale = scale,
        translationX = translationX,
        translationY = translationY,
        medallionCenterX = MOBILE_4D_MEDALLION_CENTER_X * scale + translationX,
        medallionCenterY = MOBILE_4D_MEDALLION_CENTER_Y * scale + translationY,
        medallionRadiusX = MOBILE_4D_MEDALLION_RADIUS_X * scale,
        medallionRadiusY = MOBILE_4D_MEDALLION_RADIUS_Y * scale,
    )
}

internal fun mobile4DLightMix(
    tiltX: Float,
    neutralSide: Mobile4DLightSide,
): Mobile4DLightMix {
    val x = tiltX.coerceIn(-1f, 1f)
    val activeSide = when {
        x < 0f -> Mobile4DLightSide.Left
        x > 0f -> Mobile4DLightSide.Right
        else -> neutralSide
    }

    return Mobile4DLightMix(
        activeSide = activeSide,
        centerWeight = 1f - abs(x),
        sideWeight = abs(x),
    )
}

internal fun mobile4DActiveLightSide(
    tiltX: Float,
    previousSide: Mobile4DLightSide,
): Mobile4DLightSide = when (previousSide) {
    Mobile4DLightSide.Left ->
        if (tiltX.coerceIn(-1f, 1f) >= MOBILE_4D_LIGHT_SIDE_SWITCH_THRESHOLD) {
            Mobile4DLightSide.Right
        } else {
            Mobile4DLightSide.Left
        }
    Mobile4DLightSide.Right ->
        if (tiltX.coerceIn(-1f, 1f) <= -MOBILE_4D_LIGHT_SIDE_SWITCH_THRESHOLD) {
            Mobile4DLightSide.Left
        } else {
            Mobile4DLightSide.Right
        }
}

internal fun mobile4DParallaxOffset(
    layer: Mobile4DParallaxLayer,
    tiltX: Float,
    tiltY: Float,
): Mobile4DParallaxOffset = Mobile4DParallaxOffset(
    xDp = tiltX.coerceIn(-1f, 1f) * layer.maximumDepthDp,
    yDp = tiltY.coerceIn(-1f, 1f) * layer.maximumDepthDp,
)

internal fun mobile4DEyeState(
    connected: Boolean,
    connecting: Boolean,
): Mobile4DEyeState = when {
    connecting -> Mobile4DEyeState.Connecting
    connected -> Mobile4DEyeState.Connected
    else -> Mobile4DEyeState.Disconnected
}

private const val MOBILE_4D_MASTER_WIDTH = 2160f
private const val MOBILE_4D_MASTER_HEIGHT = 4670f
private const val MOBILE_4D_MEDALLION_CENTER_X = MOBILE_4D_MASTER_WIDTH * 430f / 853f
private const val MOBILE_4D_MEDALLION_CENTER_Y = MOBILE_4D_MASTER_HEIGHT * 711f / 1844f
private const val MOBILE_4D_MEDALLION_RADIUS_X = MOBILE_4D_MASTER_WIDTH * 260f / 853f
private const val MOBILE_4D_MEDALLION_RADIUS_Y = MOBILE_4D_MASTER_HEIGHT * 260f / 1844f
private const val MOBILE_4D_LIGHT_SIDE_SWITCH_THRESHOLD = 0.15f

package com.maestrovpn.tv.compose.screen.tvhome

import kotlin.math.abs
import kotlin.math.ceil
import kotlin.math.exp

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
    // Резной веер протоколов смонтирован на дерево поверх лоз, но ближе к зрителю их
    // и дальше медальона: он не должен «плавать» сильнее кольца при наклоне.
    Arc(4f),
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

internal data class Mobile4DTiltVector(
    val x: Float,
    val y: Float,
) {
    companion object {
        val Zero = Mobile4DTiltVector(0f, 0f)
    }
}

internal enum class Mobile4DDisplayRotation {
    Rotation0,
    Rotation90,
    Rotation180,
    Rotation270,
}

internal enum class Mobile4DSceneMode {
    Home,
    Internal,
}

internal enum class Mobile4DAssetRetention(val residentLightCount: Int) {
    None(0),
    CentreOnly(1),
    CentreAndActiveSide(2),
    AllLights(3),
}

internal data class Mobile4DAssetMemoryPolicy(
    val targetWidthPx: Int,
    val retention: Mobile4DAssetRetention,
    val decodedArtBudgetBytes: Long,
    val estimatedBytesPerLight: Long,
    val estimatedResidentBytes: Long,
)

/** Owns decoded items until the caller explicitly completes a successful handoff. */
internal class Mobile4DOwnedBatch<T>(
    private val release: (T) -> Unit,
) {
    private val items = mutableListOf<T>()
    private var handedOff = false
    private var released = false

    fun add(item: T) {
        check(!handedOff && !released) { "Cannot add to a completed mobile 4D batch" }
        try {
            items.add(item)
        } catch (error: Throwable) {
            try {
                release(item)
            } catch (releaseError: Throwable) {
                error.addSuppressed(releaseError)
            }
            throw error
        }
    }

    fun handOff(): List<T> {
        check(!handedOff && !released) { "Mobile 4D batch is already completed" }
        handedOff = true
        return items
    }

    fun releaseUnlessHandedOff() {
        if (handedOff || released) return
        released = true
        var firstError: Throwable? = null
        for (index in items.indices.reversed()) {
            try {
                release(items[index])
            } catch (error: Throwable) {
                val recordedError = firstError
                if (recordedError == null) firstError = error else recordedError.addSuppressed(error)
            }
        }
        items.clear()
        firstError?.let { throw it }
    }
}

internal fun mobile4DAllocationsFitBudget(
    allocationByteCounts: Iterable<Long>,
    budgetBytes: Long,
): Boolean = mobile4DAllocationsFitBudget(allocationByteCounts, budgetBytes) { it }

internal fun <T> mobile4DAllocationsFitBudget(
    allocations: Iterable<T>,
    budgetBytes: Long,
    allocationByteCount: (T) -> Long,
): Boolean {
    if (budgetBytes < 0L) return false
    var total = 0L
    for (allocation in allocations) {
        val allocationBytes = allocationByteCount(allocation)
        if (allocationBytes < 0L || allocationBytes > budgetBytes - total) return false
        total += allocationBytes
    }
    return true
}

internal fun <T, R> mobile4DWithRetainedReferences(
    references: List<T>,
    retain: (T) -> Unit,
    release: (T) -> Unit,
    block: () -> R,
): R {
    var retainedCount = 0
    try {
        while (retainedCount < references.size) {
            retain(references[retainedCount])
            retainedCount += 1
        }
        return block()
    } catch (error: Throwable) {
        for (index in retainedCount - 1 downTo 0) {
            try {
                release(references[index])
            } catch (rollbackError: Throwable) {
                error.addSuppressed(rollbackError)
            }
        }
        throw error
    }
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

internal fun mobile4DRemapForDisplayRotation(
    tilt: Mobile4DTiltVector,
    rotation: Mobile4DDisplayRotation,
): Mobile4DTiltVector = when (rotation) {
    Mobile4DDisplayRotation.Rotation0 -> tilt
    Mobile4DDisplayRotation.Rotation90 -> Mobile4DTiltVector(x = -tilt.y, y = tilt.x)
    Mobile4DDisplayRotation.Rotation180 -> Mobile4DTiltVector(x = -tilt.x, y = -tilt.y)
    Mobile4DDisplayRotation.Rotation270 -> Mobile4DTiltVector(x = tilt.y, y = -tilt.x)
}

internal fun mobile4DNormalizeTiltDegrees(degrees: Float): Float {
    val magnitude = abs(degrees)
    if (magnitude <= MOBILE_4D_TILT_DEAD_ZONE_DEGREES) return 0f

    val normalizedMagnitude = (
        (magnitude.coerceAtMost(MOBILE_4D_TILT_LIMIT_DEGREES) - MOBILE_4D_TILT_DEAD_ZONE_DEGREES) /
            (MOBILE_4D_TILT_LIMIT_DEGREES - MOBILE_4D_TILT_DEAD_ZONE_DEGREES)
        ).coerceIn(0f, 1f)
    return if (degrees < 0f) -normalizedMagnitude else normalizedMagnitude
}

internal fun mobile4DLowPass(
    previous: Mobile4DTiltVector,
    target: Mobile4DTiltVector,
    elapsedMillis: Long,
    timeConstantMillis: Float = MOBILE_4D_TILT_FILTER_TIME_CONSTANT_MILLIS,
): Mobile4DTiltVector {
    if (elapsedMillis <= 0L || timeConstantMillis <= 0f) return previous
    val alpha = (1.0 - exp(-elapsedMillis.toDouble() / timeConstantMillis.toDouble())).toFloat()
    return Mobile4DTiltVector(
        x = previous.x + (target.x - previous.x) * alpha,
        y = previous.y + (target.y - previous.y) * alpha,
    )
}

internal fun mobile4DTargetWidthBucket(
    viewportWidthPx: Int,
    isLowRamDevice: Boolean,
): Int {
    val maximumWidth = if (isLowRamDevice) {
        Mobile4DGeneratedAssets.lowRamMaximumTargetWidthPx
    } else {
        Mobile4DGeneratedAssets.maximumTargetWidthPx
    }
    val positiveWidth = viewportWidthPx.coerceAtLeast(MOBILE_4D_MINIMUM_TARGET_WIDTH_PX)
    val roundedUp = (
        ceil(positiveWidth.toDouble() / Mobile4DGeneratedAssets.targetWidthStepPx) *
            Mobile4DGeneratedAssets.targetWidthStepPx
        ).toInt()
    return roundedUp.coerceAtMost(maximumWidth)
}

/**
 * Chooses the largest density bucket that keeps decoded home art inside the generated
 * manifest's memory-class fraction. The budget reserves 8 MiB for the existing eye bitmaps.
 */
internal fun mobile4DAssetMemoryPolicy(
    viewportWidthPx: Int,
    memoryClassMiB: Int,
    isLowRamDevice: Boolean,
    relightingEnabled: Boolean,
    sceneMode: Mobile4DSceneMode,
): Mobile4DAssetMemoryPolicy {
    val memoryClassBytes = memoryClassMiB.coerceAtLeast(0).toLong() * MOBILE_4D_BYTES_PER_MEBIBYTE
    val decodedArtBudget = (
        memoryClassBytes * Mobile4DGeneratedAssets.maximumMemoryClassFraction
        ).toLong().minus(MOBILE_4D_EYE_RESERVE_BYTES).coerceAtLeast(0L)
    if (sceneMode == Mobile4DSceneMode.Internal) {
        return Mobile4DAssetMemoryPolicy(
            targetWidthPx = 0,
            retention = Mobile4DAssetRetention.None,
            decodedArtBudgetBytes = decodedArtBudget,
            estimatedBytesPerLight = 0L,
            estimatedResidentBytes = 0L,
        )
    }

    val requestedBucket = mobile4DTargetWidthBucket(viewportWidthPx, isLowRamDevice)
    val requestedRetention = when {
        !relightingEnabled -> Mobile4DAssetRetention.CentreOnly
        isLowRamDevice && !Mobile4DGeneratedAssets.lowRamTiltRelightingEnabled ->
            Mobile4DAssetRetention.CentreOnly
        else -> Mobile4DAssetRetention.AllLights
    }
    if (requestedRetention == Mobile4DAssetRetention.AllLights) {
        val allLightsBytes = mobile4DEstimatedBytesPerLight(requestedBucket) *
            Mobile4DAssetRetention.AllLights.residentLightCount
        if (allLightsBytes <= decodedArtBudget) {
            return Mobile4DAssetMemoryPolicy(
                targetWidthPx = requestedBucket,
                retention = Mobile4DAssetRetention.AllLights,
                decodedArtBudgetBytes = decodedArtBudget,
                estimatedBytesPerLight = allLightsBytes / Mobile4DAssetRetention.AllLights.residentLightCount,
                estimatedResidentBytes = allLightsBytes,
            )
        }
    }

    val fallbackRetention = if (requestedRetention == Mobile4DAssetRetention.CentreOnly) {
        Mobile4DAssetRetention.CentreOnly
    } else {
        Mobile4DAssetRetention.CentreAndActiveSide
    }
    var bucket = requestedBucket
    var bytesPerLight = mobile4DEstimatedBytesPerLight(bucket)
    while (
        bytesPerLight * fallbackRetention.residentLightCount > decodedArtBudget &&
        bucket > MOBILE_4D_MINIMUM_TARGET_WIDTH_PX
    ) {
        bucket = mobile4DNextLowerTargetWidthBucket(bucket)
        bytesPerLight = mobile4DEstimatedBytesPerLight(bucket)
    }
    if (bytesPerLight * fallbackRetention.residentLightCount > decodedArtBudget) {
        return Mobile4DAssetMemoryPolicy(
            targetWidthPx = 0,
            retention = Mobile4DAssetRetention.None,
            decodedArtBudgetBytes = decodedArtBudget,
            estimatedBytesPerLight = 0L,
            estimatedResidentBytes = 0L,
        )
    }
    return Mobile4DAssetMemoryPolicy(
        targetWidthPx = bucket,
        retention = fallbackRetention,
        decodedArtBudgetBytes = decodedArtBudget,
        estimatedBytesPerLight = bytesPerLight,
        estimatedResidentBytes = bytesPerLight * fallbackRetention.residentLightCount,
    )
}

private fun mobile4DEstimatedBytesPerLight(targetWidthPx: Int): Long {
    val scale = targetWidthPx.toDouble() / Mobile4DGeneratedAssets.masterWidth.toDouble()
    return Mobile4DGeneratedAssets.pages
        .asSequence()
        .filter { it.light == Mobile4DAssetLight.Centre }
        .sumOf { page ->
            val width = ceil(page.width * scale).toLong()
            val height = ceil(page.height * scale).toLong()
            width * height * MOBILE_4D_ARGB_BYTES_PER_PIXEL
        }
}

private fun mobile4DNextLowerTargetWidthBucket(currentWidthPx: Int): Int {
    val step = Mobile4DGeneratedAssets.targetWidthStepPx
    val next = if (currentWidthPx % step == 0) currentWidthPx - step else currentWidthPx / step * step
    return next.coerceAtLeast(MOBILE_4D_MINIMUM_TARGET_WIDTH_PX)
}

private const val MOBILE_4D_MASTER_WIDTH = 2160f
private const val MOBILE_4D_MASTER_HEIGHT = 4670f
private const val MOBILE_4D_MEDALLION_CENTER_X = MOBILE_4D_MASTER_WIDTH * 430f / 853f
private const val MOBILE_4D_MEDALLION_CENTER_Y = MOBILE_4D_MASTER_HEIGHT * 711f / 1844f
private const val MOBILE_4D_MEDALLION_RADIUS_X = MOBILE_4D_MASTER_WIDTH * 260f / 853f
private const val MOBILE_4D_MEDALLION_RADIUS_Y = MOBILE_4D_MASTER_HEIGHT * 260f / 1844f
private const val MOBILE_4D_LIGHT_SIDE_SWITCH_THRESHOLD = 0.15f
private const val MOBILE_4D_TILT_LIMIT_DEGREES = 12f
private const val MOBILE_4D_TILT_DEAD_ZONE_DEGREES = 0.5f
private const val MOBILE_4D_TILT_FILTER_TIME_CONSTANT_MILLIS = 180f
private const val MOBILE_4D_MINIMUM_TARGET_WIDTH_PX = 64
private const val MOBILE_4D_ARGB_BYTES_PER_PIXEL = 4L
private const val MOBILE_4D_BYTES_PER_MEBIBYTE = 1024L * 1024L
private const val MOBILE_4D_EYE_RESERVE_BYTES = 8L * MOBILE_4D_BYTES_PER_MEBIBYTE

package com.maestrovpn.tv.compose.screen.tvhome

/** Source pixels in the 360-square crop of the owner's 2026-09-06 reference. */
internal data class ReferenceEyeMargin(val x: Float, val upper: Float, val lower: Float)

internal val REFERENCE_EYE_MARGINS = listOf(
    ReferenceEyeMargin(20f, 184f, 184f),
    ReferenceEyeMargin(40f, 171f, 198f),
    ReferenceEyeMargin(60f, 156f, 205f),
    ReferenceEyeMargin(80f, 144f, 216f),
    ReferenceEyeMargin(100f, 132f, 223f),
    ReferenceEyeMargin(120f, 125f, 229f),
    ReferenceEyeMargin(140f, 123f, 231f),
    ReferenceEyeMargin(160f, 123f, 232f),
    ReferenceEyeMargin(180f, 124f, 233f),
    ReferenceEyeMargin(200f, 125f, 232f),
    ReferenceEyeMargin(220f, 131f, 230f),
    ReferenceEyeMargin(240f, 140f, 227f),
    ReferenceEyeMargin(260f, 153f, 222f),
    ReferenceEyeMargin(280f, 166f, 216f),
    ReferenceEyeMargin(300f, 178f, 209f),
    ReferenceEyeMargin(320f, 188f, 203f),
    ReferenceEyeMargin(338f, 198f, 198f),
)

internal fun referenceEyeMargin(x: Float, closure: Float): ReferenceEyeMargin {
    val rightIndex = REFERENCE_EYE_MARGINS.indexOfFirst { it.x >= x }
    val margin = when {
        rightIndex == 0 -> REFERENCE_EYE_MARGINS.first().copy(x = x)
        rightIndex < 0 -> REFERENCE_EYE_MARGINS.last().copy(x = x)
        else -> {
            val left = REFERENCE_EYE_MARGINS[rightIndex - 1]
            val right = REFERENCE_EYE_MARGINS[rightIndex]
            val t = (x - left.x) / (right.x - left.x)
            ReferenceEyeMargin(x, left.upper + (right.upper - left.upper) * t,
                left.lower + (right.lower - left.lower) * t)
        }
    }
    val phase = closure.coerceIn(0f, 1f)
    val seam = margin.upper * 0.25f + margin.lower * 0.75f
    return ReferenceEyeMargin(x, margin.upper + (seam - margin.upper) * phase,
        margin.lower + (seam - margin.lower) * phase)
}

/** Stretch the same lid texture toward its contact seam; socket edges never move. */
internal fun referenceEyeWarpY(x: Float, y: Float, closure: Float): Float {
    val open = referenceEyeMargin(x, 0f)
    val current = referenceEyeMargin(x, closure)
    return referenceEyeWarpBetween(y, open.upper, open.lower, current.upper, current.lower)
}

internal fun referenceEyeWarpBetween(
    y: Float, openUpper: Float, openLower: Float, currentUpper: Float, currentLower: Float,
): Float {
    return when {
        y <= openUpper -> y * currentUpper / openUpper
        y >= openLower -> REFERENCE_EYE_SIZE - (REFERENCE_EYE_SIZE - y) *
            (REFERENCE_EYE_SIZE - currentLower) / (REFERENCE_EYE_SIZE - openLower)
        else -> currentUpper + (y - openUpper) / (openLower - openUpper) *
            (currentLower - currentUpper)
    }
}

internal const val REFERENCE_EYE_SIZE = 360f
// Registered material guard radius 650 on the 2160-wide 4D master; the canvas
// radius is 2160 * 260 / 853. This covers the old surround up to the bronze rim.
internal const val REFERENCE_EYE_SOCKET_FRACTION = 650f / (2f * 2160f * 260f / 853f)

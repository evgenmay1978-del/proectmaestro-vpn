package com.maestrovpn.tv.compose.screen.tvhome

internal data class ReferenceEyeMargin(val x: Float, val upper: Float, val lower: Float)

/**
 * Two C1 cubic arcs share X control points. The first/last vertical tangents round
 * the canthi; equal horizontal tangents at the centre avoid a polygonal plateau.
 * Coordinates are measured from the generated anatomy reference in a 360-square fit.
 */
internal val REFERENCE_EYE_CONTROLS = listOf(
    ReferenceEyeMargin(16f, 186f, 186f),
    ReferenceEyeMargin(52f, 168f, 201f),
    ReferenceEyeMargin(108f, 124f, 230f),
    ReferenceEyeMargin(180f, 124f, 230f),
    ReferenceEyeMargin(252f, 124f, 230f),
    ReferenceEyeMargin(310f, 166f, 201f),
    ReferenceEyeMargin(344f, 187f, 187f),
)

private fun eyeCubic(a: Float, b: Float, c: Float, d: Float, t: Float): Float {
    val u = 1f - t
    return u * u * u * a + 3f * u * u * t * b + 3f * u * t * t * c + t * t * t * d
}

internal fun referenceEyeMargin(x: Float, closure: Float): ReferenceEyeMargin {
    val controls = REFERENCE_EYE_CONTROLS
    val open = when {
        x <= controls.first().x -> controls.first().copy(x = x)
        x >= controls.last().x -> controls.last().copy(x = x)
        else -> {
            val offset = if (x <= controls[3].x) 0 else 3
            val a = controls[offset]
            val b = controls[offset + 1]
            val c = controls[offset + 2]
            val d = controls[offset + 3]
            var lo = 0f
            var hi = 1f
            repeat(20) {
                val t = (lo + hi) * 0.5f
                if (eyeCubic(a.x, b.x, c.x, d.x, t) < x) lo = t else hi = t
            }
            val t = (lo + hi) * 0.5f
            ReferenceEyeMargin(x, eyeCubic(a.upper, b.upper, c.upper, d.upper, t),
                eyeCubic(a.lower, b.lower, c.lower, d.lower, t))
        }
    }
    val phase = closure.coerceIn(0f, 1f)
    val seam = open.upper * 0.25f + open.lower * 0.75f
    return ReferenceEyeMargin(x, open.upper + (seam - open.upper) * phase,
        open.lower + (seam - open.lower) * phase)
}

internal val REFERENCE_EYE_MARGINS: List<ReferenceEyeMargin> =
    (0..82).map { referenceEyeMargin(16f + it * 4f, 0f) }

internal fun referenceEyeWarpY(x: Float, y: Float, closure: Float): Float {
    val open = referenceEyeMargin(x, 0f)
    val current = referenceEyeMargin(x, closure)
    return referenceEyeWarpBetween(y, open.upper, open.lower, current.upper, current.lower)
}

/** Translate the upper texture without stretching its grain; the renderer mirrors its top edge. */
internal fun referenceEyeWarpBetween(
    y: Float, openUpper: Float, openLower: Float, currentUpper: Float, currentLower: Float,
): Float {
    fun smooth(t: Float): Float {
        val v = t.coerceIn(0f, 1f)
        return v * v * (3f - 2f * v)
    }
    return when {
        y <= openUpper -> y + (currentUpper - openUpper)
        y >= openLower -> y + (currentLower - openLower) *
            smooth((openLower + REFERENCE_EYE_LOWER_BAND - y) / REFERENCE_EYE_LOWER_BAND)
        else -> currentUpper + (y - openUpper) / (openLower - openUpper) *
            (currentLower - currentUpper)
    }
}

internal const val REFERENCE_EYE_LOWER_BAND = 44f
internal const val REFERENCE_EYE_SIZE = 360f
// Same registered material guard and frame; no change to the Mobile4D viewport.
internal const val REFERENCE_EYE_SOCKET_FRACTION = 650f / (2f * 2160f * 260f / 853f)

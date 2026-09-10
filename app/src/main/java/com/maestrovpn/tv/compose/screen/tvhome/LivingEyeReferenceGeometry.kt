package com.maestrovpn.tv.compose.screen.tvhome

internal data class ReferenceEyeMargin(val x: Float, val upper: Float, val lower: Float)

// Contact curve measured in the complete 1254x1254 closed source; paired cubic X controls are below.
internal val REFERENCE_EYE_CLOSED_SEAM_Y =
    listOf(651.93f, 657.06f, 727f, 727f, 727f, 670.41f, 663.40f)
        .map { it * REFERENCE_EYE_SIZE / 1254f }

/**
 * Two C1 cubic arcs share X control points. The first/last vertical tangents round
 * the canthi; equal horizontal tangents at the centre avoid a polygonal plateau.
 * Coordinates are measured from the generated anatomy reference in a 360-square fit.
 */
internal val REFERENCE_EYE_CONTROLS = listOf(
    ReferenceEyeMargin(16f, REFERENCE_EYE_CLOSED_SEAM_Y.first(), REFERENCE_EYE_CLOSED_SEAM_Y.first()),
    ReferenceEyeMargin(52f, 168f, 201f),
    ReferenceEyeMargin(108f, 124f, 230f),
    ReferenceEyeMargin(180f, 124f, 230f),
    ReferenceEyeMargin(252f, 124f, 230f),
    ReferenceEyeMargin(310f, 166f, 201f),
    ReferenceEyeMargin(344f, REFERENCE_EYE_CLOSED_SEAM_Y.last(), REFERENCE_EYE_CLOSED_SEAM_Y.last()),
)

private fun eyeCubic(a: Float, b: Float, c: Float, d: Float, t: Float): Float {
    val u = 1f - t
    return u * u * u * a + 3f * u * u * t * b + 3f * u * t * t * c + t * t * t * d
}

internal fun referenceEyeMargin(x: Float, closure: Float): ReferenceEyeMargin {
    val controls = REFERENCE_EYE_CONTROLS
    var seam = REFERENCE_EYE_CLOSED_SEAM_Y.first()
    val open = when {
        x <= controls.first().x -> controls.first().copy(x = x)
        x >= controls.last().x -> {
            seam = REFERENCE_EYE_CLOSED_SEAM_Y.last()
            controls.last().copy(x = x)
        }
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
            seam = eyeCubic(REFERENCE_EYE_CLOSED_SEAM_Y[offset], REFERENCE_EYE_CLOSED_SEAM_Y[offset + 1],
                REFERENCE_EYE_CLOSED_SEAM_Y[offset + 2], REFERENCE_EYE_CLOSED_SEAM_Y[offset + 3], t)
            ReferenceEyeMargin(x, eyeCubic(a.upper, b.upper, c.upper, d.upper, t),
                eyeCubic(a.lower, b.lower, c.lower, d.lower, t))
        }
    }
    val phase = closure.coerceIn(0f, 1f)
    if (phase == 1f) return ReferenceEyeMargin(x, seam, seam)
    return ReferenceEyeMargin(x, open.upper + (seam - open.upper) * phase,
        open.lower + (seam - open.lower) * phase)
}

internal val REFERENCE_EYE_MARGINS: List<ReferenceEyeMargin> =
    (0..82).map { referenceEyeMargin(16f + it * 4f, 0f) }

internal fun referenceEyeWarpY(x: Float, y: Float, closure: Float): Float {
    val open = referenceEyeMargin(x, 0f)
    val sourceSeam = referenceEyeMargin(x, 1f).upper
    val current = referenceEyeMargin(x, closure)
    return referenceEyeWarpBetween(y, open.upper, open.lower, sourceSeam, current.upper, current.lower)
}

/** Fold one closed surface around its measured seam; the upper crease and outer skin stay fixed. */
internal fun referenceEyeWarpBetween(
    y: Float, openUpper: Float, openLower: Float, sourceSeam: Float, currentUpper: Float, currentLower: Float,
): Float {
    if (currentUpper == currentLower) return y
    return when {
        y <= openUpper || y >= openLower -> y
        y < sourceSeam -> openUpper + (y - openUpper) / (sourceSeam - openUpper) *
            (currentUpper - openUpper)
        else -> currentLower + (y - sourceSeam) / (openLower - sourceSeam) *
            (openLower - currentLower)
    }
}

internal const val REFERENCE_EYE_SIZE = 360f
// Same registered material guard and frame; no change to the Mobile4D viewport.
internal const val REFERENCE_EYE_SOCKET_FRACTION = 650f / (2f * 2160f * 260f / 853f)

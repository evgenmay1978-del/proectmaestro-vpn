package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlin.math.cos
import kotlin.math.sin

/**
 * Country flags of the server rows as a real circle.
 *
 * An emoji flag is a wide rectangle: drawn inside the golden ring it left a visible gap all around
 * and never filled the badge (owner's screen, 04.11.2026). Countries whose flag is a simple
 * construction are drawn here stroke for stroke; everything else falls back to the emoji, but the
 * badge still crops it to the circle.
 */

private const val REGIONAL_START = 0x1F1E6

/** ISO-3166 alpha-2 of an emoji flag ("🇪🇸" → "ES"); empty when the text is not a flag pair. */
fun flagCountryCode(flag: String): String {
    if (flag.length < 2) return ""
    val first = flag.codePointAt(0)
    if (first < REGIONAL_START || first > REGIONAL_START + 25) return ""
    val second = flag.codePointAt(Character.charCount(first))
    if (second < REGIONAL_START || second > REGIONAL_START + 25) return ""
    return buildString(2) {
        append('A' + (first - REGIONAL_START))
        append('A' + (second - REGIONAL_START))
    }
}

/** First emoji flag inside a server label, or "" when the label carries none. */
fun flagEmojiOf(text: String): String {
    var i = 0
    while (i < text.length) {
        val cp = text.codePointAt(i)
        if (cp in REGIONAL_START..REGIONAL_START + 25) {
            val next = i + Character.charCount(cp)
            if (next < text.length) {
                val cp2 = text.codePointAt(next)
                if (cp2 in REGIONAL_START..REGIONAL_START + 25) {
                    return text.substring(i, next + Character.charCount(cp2))
                }
            }
            return ""
        }
        i += Character.charCount(cp)
    }
    return ""
}

/**
 * Pictographs the whitelist labels carry next to the country ("🌀 Раю - Эстония ⚡"): they are
 * panel decoration, not part of the server name, so a row drops them instead of printing raw
 * symbols into the title.
 */
private fun isDecoration(cp: Int): Boolean =
    cp in 0x1F000..0x1FAFF ||
        cp in 0x2600..0x27BF ||
        cp in 0x2B00..0x2BFF ||
        cp in 0xFE00..0xFE0F ||
        cp == 0x200D ||
        cp == 0x20E3

/** Label without pictographs, with the spaces they leave behind collapsed. */
fun stripServerDecorations(text: String): String {
    val out = StringBuilder(text.length)
    var i = 0
    while (i < text.length) {
        val cp = text.codePointAt(i)
        if (!isDecoration(cp)) out.appendCodePoint(cp)
        i += Character.charCount(cp)
    }
    return out.toString().replace(Regex("\\s+"), " ").trim()
}

private data class FlagDesign(
    val bands: List<Color> = emptyList(),
    val weights: List<Float>? = null,
    val vertical: Boolean = false,
    val nordic: Pair<Color, Color>? = null,
    val swiss: Boolean = false,
    val japan: Boolean = false,
    val czech: Boolean = false,
    val eu: Boolean = false,
)

private fun h(vararg colors: Long) = FlagDesign(bands = colors.map { Color(it) })

private fun hv(vararg colors: Long) = FlagDesign(bands = colors.map { Color(it) }, vertical = true)

private val FLAGS: Map<String, FlagDesign> = mapOf(
    "ES" to FlagDesign(bands = listOf(Color(0xFFC60B1E), Color(0xFFF1BF00), Color(0xFFC60B1E)),
        weights = listOf(1f, 2f, 1f)),
    "NL" to h(0xFFAE1C28, 0xFFFFFFFF, 0xFF21468B),
    "LU" to h(0xFFEF3340, 0xFFFFFFFF, 0xFF00A2E1),
    "RU" to h(0xFFFFFFFF, 0xFF0033A0, 0xFFDA291C),
    "AT" to h(0xFFED2939, 0xFFFFFFFF, 0xFFED2939),
    "EE" to h(0xFF0072CE, 0xFF000000, 0xFFFFFFFF),
    "DE" to h(0xFF000000, 0xFFDD0000, 0xFFFFCE00),
    "LV" to FlagDesign(bands = listOf(Color(0xFF9E3039), Color(0xFFFFFFFF), Color(0xFF9E3039)),
        weights = listOf(2f, 1f, 2f)),
    "PL" to h(0xFFFFFFFF, 0xFFDC143C),
    "SK" to h(0xFFFFFFFF, 0xFF0B4EA2, 0xFFEE1C25),
    "SI" to h(0xFFFFFFFF, 0xFF005DA4, 0xFFED1C24),
    "IT" to hv(0xFF008C45, 0xFFF4F5F0, 0xFFCD212A),
    "FR" to hv(0xFF0055A4, 0xFFFFFFFF, 0xFFEF4135),
    "BE" to hv(0xFF000000, 0xFFFDDA24, 0xFFEF3340),
    "RO" to hv(0xFF002B7F, 0xFFFCD116, 0xFFCE1126),
    "MD" to hv(0xFF0046AE, 0xFFFCD116, 0xFFCC092F),
    "IE" to hv(0xFF169B62, 0xFFFFFFFF, 0xFFFF883E),
    "SE" to FlagDesign(nordic = Color(0xFF006AA7) to Color(0xFFFECC02)),
    "FI" to FlagDesign(nordic = Color(0xFFFFFFFF) to Color(0xFF003580)),
    "DK" to FlagDesign(nordic = Color(0xFFC8102E) to Color(0xFFFFFFFF)),
    "NO" to FlagDesign(nordic = Color(0xFFBA0C2F) to Color(0xFFFFFFFF)),
    "IS" to FlagDesign(nordic = Color(0xFF02529C) to Color(0xFFFFFFFF)),
    "CH" to FlagDesign(swiss = true),
    "JP" to FlagDesign(japan = true),
    "CZ" to FlagDesign(czech = true),
    "EU" to FlagDesign(eu = true),
)

private fun DrawScope.drawFlag(design: FlagDesign) {
    val w = size.width
    val h = size.height
    if (design.bands.isNotEmpty()) {
        val weights = design.weights ?: List(design.bands.size) { 1f }
        val total = weights.sum()
        if (design.vertical) {
            var x = 0f
            design.bands.forEachIndexed { index, color ->
                val bw = w * weights[index] / total
                drawRect(color, Offset(x, 0f), Size(bw, h))
                x += bw
            }
        } else {
            var y = 0f
            design.bands.forEachIndexed { index, color ->
                val bh = h * weights[index] / total
                drawRect(color, Offset(0f, y), Size(w, bh))
                y += bh
            }
        }
    }
    design.nordic?.let { (background, cross) ->
        drawRect(background, Offset.Zero, Size(w, h))
        val t = w * 0.16f
        drawRect(cross, Offset(w * 0.30f - t / 2f, 0f), Size(t, h))
        drawRect(cross, Offset(0f, h / 2f - t / 2f), Size(w, t))
    }
    if (design.swiss) {
        drawRect(Color(0xFFD52B1E), Offset.Zero, Size(w, h))
        val t = w * 0.18f
        drawRect(Color.White, Offset(w / 2f - t / 2f, h * 0.22f), Size(t, h * 0.56f))
        drawRect(Color.White, Offset(w * 0.22f, h / 2f - t / 2f), Size(w * 0.56f, t))
    }
    if (design.japan) {
        drawRect(Color.White, Offset.Zero, Size(w, h))
        drawCircle(Color(0xFFBC002D), radius = w * 0.28f, center = Offset(w / 2f, h / 2f))
    }
    if (design.czech) {
        drawRect(Color.White, Offset.Zero, Size(w, h / 2f))
        drawRect(Color(0xFFD7141A), Offset(0f, h / 2f), Size(w, h / 2f))
        val triangle = Path().apply {
            moveTo(0f, 0f)
            lineTo(w * 0.55f, h / 2f)
            lineTo(0f, h)
            close()
        }
        drawPath(triangle, Color(0xFF11457E))
    }
    if (design.eu) {
        drawRect(Color(0xFF003399), Offset.Zero, Size(w, h))
        val radius = w * 0.30f
        val dot = w * 0.035f
        repeat(12) { index ->
            val angle = Math.toRadians(index * 30.0)
            drawCircle(
                Color(0xFFFFCC00),
                radius = dot,
                center = Offset(
                    w / 2f + (radius * cos(angle)).toFloat(),
                    h / 2f + (radius * sin(angle)).toFloat(),
                ),
            )
        }
    }
}

/**
 * Round country badge of a server row: the flag fills the whole circle, the golden ring sits on the
 * rim. Without a flag the row keeps its role icon.
 */
@Composable
fun CountryFlagBadge(
    flag: String,
    icon: ImageVector,
    modifier: Modifier = Modifier,
    size: Dp = 30.dp,
) {
    val design = FLAGS[flagCountryCode(flag)]
    Box(modifier.size(size).clip(CircleShape), contentAlignment = Alignment.Center) {
        when {
            design != null -> Canvas(Modifier.fillMaxSize()) { drawFlag(design) }
            flag.isNotEmpty() -> Box(
                Modifier.fillMaxSize().background(Color(0xFF2A1C10)),
                contentAlignment = Alignment.Center,
            ) {
                Text(flag, fontSize = (size.value * 1.35f).sp, maxLines = 1, softWrap = false)
            }
            else -> Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(size * 0.8f))
        }
        Box(Modifier.fillMaxSize().border(1.dp, PremiumGold.copy(alpha = 0.85f), CircleShape))
    }
}

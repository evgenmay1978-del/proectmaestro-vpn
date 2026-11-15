package com.maestrovpn.tv.compose.premium

/*
 * Text helpers of the server labels: the flag of a label, its ISO code, and the label without the
 * panel's pictographs. Plain Kotlin (no Compose) so the label logic is unit-tested on the JVM.
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

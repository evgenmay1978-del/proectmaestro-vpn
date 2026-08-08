package com.maestrovpn.tv.compose.premium

internal enum class MobilePremiumLayoutMode {
    Compact,
    Regular,
    Expanded,
}

internal fun mobilePremiumLayoutMode(
    widthDp: Int,
    heightDp: Int,
): MobilePremiumLayoutMode = when {
    // ⛔ ЛОВУШКА: гейт Expanded обязан смотреть на КОРОТКУЮ сторону, а не на ширину.
    // Любой современный телефон в ландшафте шире 600dp (Pixel 8 = 852x393), поэтому
    // при проверке `widthDp >= 600` он получал планшетные 32dp и 30sp заголовок на
    // виджете высотой 393dp, а ветка Compact становилась недостижимой — ровно то,
    // что ловил тест landscapePhoneUsesCompactLayout.
    minOf(widthDp, heightDp) >= 600 -> MobilePremiumLayoutMode.Expanded
    widthDp > heightDp -> MobilePremiumLayoutMode.Compact
    else -> MobilePremiumLayoutMode.Regular
}

internal fun mobilePremiumHorizontalPadding(mode: MobilePremiumLayoutMode): Int = when (mode) {
    MobilePremiumLayoutMode.Compact -> 12
    MobilePremiumLayoutMode.Regular -> 18
    MobilePremiumLayoutMode.Expanded -> 32
}

internal fun mobilePremiumHorizontalPadding(
    widthDp: Int,
    heightDp: Int,
): Int = mobilePremiumHorizontalPadding(
    mobilePremiumLayoutMode(widthDp = widthDp, heightDp = heightDp),
)

internal fun mobilePremiumPaymentQrSize(maxContentWidthDp: Int): Int =
    (maxContentWidthDp - MOBILE_PREMIUM_QR_CARD_PADDING_DP * 2)
        .coerceAtMost(MOBILE_PREMIUM_QR_MAX_SIZE_DP)
        .coerceAtLeast(1)

internal fun mobilePremiumMaximumContentWidth(mode: MobilePremiumLayoutMode): Int = when (mode) {
    MobilePremiumLayoutMode.Compact -> 720
    MobilePremiumLayoutMode.Regular,
    MobilePremiumLayoutMode.Expanded -> 560
}

internal fun mobilePremiumShellEnabled(isTv: Boolean): Boolean = !isTv

private const val MOBILE_PREMIUM_QR_CARD_PADDING_DP = 12
private const val MOBILE_PREMIUM_QR_MAX_SIZE_DP = 220

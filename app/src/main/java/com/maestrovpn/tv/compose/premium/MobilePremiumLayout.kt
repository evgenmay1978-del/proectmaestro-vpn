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
    widthDp >= 600 -> MobilePremiumLayoutMode.Expanded
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

private const val MOBILE_PREMIUM_QR_CARD_PADDING_DP = 12
private const val MOBILE_PREMIUM_QR_MAX_SIZE_DP = 220

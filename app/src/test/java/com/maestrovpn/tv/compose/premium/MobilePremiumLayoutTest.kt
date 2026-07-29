package com.maestrovpn.tv.compose.premium

import org.junit.Assert.assertEquals
import org.junit.Test

class MobilePremiumLayoutTest {
    @Test
    fun narrowPortraitPurchaseUsesRegularResponsivePadding() {
        assertEquals(
            18,
            mobilePremiumHorizontalPadding(widthDp = 320, heightDp = 568),
        )
    }

    @Test
    fun portraitPhoneUsesRegularLayout() {
        assertEquals(
            MobilePremiumLayoutMode.Regular,
            mobilePremiumLayoutMode(widthDp = 393, heightDp = 852),
        )
    }

    @Test
    fun landscapePhoneUsesCompactLayout() {
        assertEquals(
            MobilePremiumLayoutMode.Compact,
            mobilePremiumLayoutMode(widthDp = 852, heightDp = 393),
        )
    }

    @Test
    fun tabletUsesExpandedLayout() {
        assertEquals(
            MobilePremiumLayoutMode.Expanded,
            mobilePremiumLayoutMode(widthDp = 800, heightDp = 1280),
        )
    }

    @Test
    fun paymentQrShrinksToRemainSquareInsideNarrowPanel() {
        assertEquals(216, mobilePremiumPaymentQrSize(maxContentWidthDp = 240))
        assertEquals(220, mobilePremiumPaymentQrSize(maxContentWidthDp = 400))
    }
}

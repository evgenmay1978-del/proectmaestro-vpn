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

    // Гейт Expanded смотрит на КОРОТКУЮ сторону. Эти два случая держат правило с обеих
    // сторон: планшет в ландшафте обязан остаться Expanded, а узкое ландшафтное окно —
    // Compact. Если кто-то поставит `widthDp > heightDp` первой ветвью, упадёт первый.
    @Test
    fun landscapeTabletStaysExpanded() {
        assertEquals(
            MobilePremiumLayoutMode.Expanded,
            mobilePremiumLayoutMode(widthDp = 1280, heightDp = 800),
        )
    }

    @Test
    fun shortLandscapeWindowUsesCompactLayout() {
        assertEquals(
            MobilePremiumLayoutMode.Compact,
            mobilePremiumLayoutMode(widthDp = 480, heightDp = 320),
        )
    }

    @Test
    fun paymentQrShrinksToRemainSquareInsideNarrowPanel() {
        assertEquals(216, mobilePremiumPaymentQrSize(maxContentWidthDp = 240))
        assertEquals(220, mobilePremiumPaymentQrSize(maxContentWidthDp = 400))
    }

    @Test
    fun shellContentWidthStaysReadableOnTablets() {
        assertEquals(560, mobilePremiumMaximumContentWidth(MobilePremiumLayoutMode.Expanded))
        assertEquals(560, mobilePremiumMaximumContentWidth(MobilePremiumLayoutMode.Regular))
        assertEquals(720, mobilePremiumMaximumContentWidth(MobilePremiumLayoutMode.Compact))
    }

    @Test
    fun premiumShellIsPhoneOnly() {
        assertEquals(true, mobilePremiumShellEnabled(isTv = false))
        assertEquals(false, mobilePremiumShellEnabled(isTv = true))
    }
}

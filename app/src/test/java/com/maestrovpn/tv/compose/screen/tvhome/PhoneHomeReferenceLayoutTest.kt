package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PhoneHomeReferenceLayoutTest {
    @Test
    fun portrait390x844MatchesOwnerLandmarks() {
        val layout = phoneHomeReferenceLayout(390f, 844f)

        assertEquals(1f, layout.heroScale, 0.03f)
        assertEquals(-58f, layout.heroTranslationY, 10f)
        assertEquals(434f, layout.deckTop, 0f)
        assertEquals(864f, layout.primaryDeckBottom, 0f)
        assertTrue(layout.requiresScroll)
        assertEquals(69f, layout.title.left, 0f)
        assertEquals(54f, layout.title.top, 0f)
        assertEquals(323f, layout.title.right, 0f)
        assertEquals(88f, layout.title.bottom, 0f)
        assertEquals(26f, layout.medallion.left, 0f)
        assertEquals(104f, layout.medallion.top, 0f)
        assertEquals(364f, layout.medallion.right, 0f)
        assertEquals(413f, layout.medallion.bottom, 0f)
        assertEquals(258.5f, layout.medallion.centerY, 0f)
        assertEquals(128f, layout.status.left, 0f)
        assertEquals(434f, layout.status.top, 0f)
        assertEquals(265f, layout.status.right, 0f)
        assertEquals(456f, layout.status.bottom, 0f)
        assertEquals(126f, layout.activeProtocol.left, 0f)
        assertEquals(456f, layout.activeProtocol.top, 0f)
        assertEquals(266f, layout.activeProtocol.right, 0f)
        assertEquals(476f, layout.activeProtocol.bottom, 0f)
        assertEquals(81f, layout.phone.left, 0f)
        assertEquals(478f, layout.phone.top, 0f)
        assertEquals(310f, layout.phone.right, 0f)
        assertEquals(516f, layout.phone.bottom, 0f)
        assertEquals(34f, layout.contacts.left, 0f)
        assertEquals(511f, layout.contacts.top, 0f)
        assertEquals(356f, layout.contacts.right, 0f)
        assertEquals(594f, layout.contacts.bottom, 0f)
        assertEquals(0f, layout.protocolArc.left, 0f)
        assertEquals(595f, layout.protocolArc.top, 0f)
        assertEquals(390f, layout.protocolArc.right, 0f)
        assertEquals(730f, layout.protocolArc.bottom, 0f)
        assertEquals(81f, layout.buy.left, 0f)
        assertEquals(724f, layout.buy.top, 0f)
        assertEquals(309f, layout.buy.right, 0f)
        assertEquals(769f, layout.buy.bottom, 0f)
        assertEquals(8f, layout.bottomConsole.left, 0f)
        assertEquals(760f, layout.bottomConsole.top, 0f)
        assertEquals(382f, layout.bottomConsole.right, 0f)
        assertEquals(864f, layout.bottomConsole.bottom, 0f)
    }

    @Test
    fun shortViewportScrollsInsteadOfShrinkingTouchTargets() {
        val layout = phoneHomeReferenceLayout(320f, 568f)

        assertTrue(layout.requiresScroll)
        assertTrue(layout.minimumInteractiveHeight >= 48f)
        assertEquals(layout.bottomConsole.bottom, layout.primaryDeckBottom, 0f)
        assertEquals(
            layout.primaryDeckBottom - layout.deckTop,
            layout.primaryDeckContentHeight,
            0f,
        )
    }

    @Test(expected = IllegalArgumentException::class)
    fun nonPositiveViewportDimensionsAreRejected() {
        phoneHomeReferenceLayout(0f, 844f)
    }

    @Test
    fun referenceScaleMapsConsoleZonesWithoutASecondInsetScale() {
        val full = phoneHomeReferenceLayout(390f, 844f)
        val narrow = phoneHomeReferenceLayout(320f, 568f)
        val left = phoneHomeReferenceBounds(
            referenceScale = full.referenceScale,
            left = 32f,
            top = 782.4f,
            right = 139f,
            bottom = 842.4f,
        )

        assertEquals(1f, full.referenceScale, 0f)
        assertEquals(320f / 390f, narrow.referenceScale, 0.000001f)
        assertEquals(32f, left.left, 0f)
        assertEquals(782.4f, left.top, 0f)
        assertEquals(139f, left.right, 0f)
        assertEquals(842.4f, left.bottom, 0f)
    }
}

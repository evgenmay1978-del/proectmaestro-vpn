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
        assertEquals(363f, layout.deckTop, 3f)
        assertTrue(layout.primaryDeckBottom <= 839f)
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
        assertEquals(363f, layout.status.top, 0f)
        assertEquals(265f, layout.status.right, 0f)
        assertEquals(386f, layout.status.bottom, 0f)
        assertEquals(126f, layout.activeProtocol.left, 0f)
        assertEquals(386f, layout.activeProtocol.top, 0f)
        assertEquals(266f, layout.activeProtocol.right, 0f)
        assertEquals(406f, layout.activeProtocol.bottom, 0f)
        assertEquals(81f, layout.phone.left, 0f)
        assertEquals(407f, layout.phone.top, 0f)
        assertEquals(310f, layout.phone.right, 0f)
        assertEquals(445f, layout.phone.bottom, 0f)
        assertEquals(84f, layout.supportNote.left, 0f)
        assertEquals(446f, layout.supportNote.top, 0f)
        assertEquals(306f, layout.supportNote.right, 0f)
        assertEquals(484f, layout.supportNote.bottom, 0f)
        assertEquals(34f, layout.contacts.left, 0f)
        assertEquals(486f, layout.contacts.top, 0f)
        assertEquals(356f, layout.contacts.right, 0f)
        assertEquals(569f, layout.contacts.bottom, 0f)
        assertEquals(0f, layout.protocolArc.left, 0f)
        assertEquals(570f, layout.protocolArc.top, 0f)
        assertEquals(390f, layout.protocolArc.right, 0f)
        assertEquals(705f, layout.protocolArc.bottom, 0f)
        assertEquals(81f, layout.buy.left, 0f)
        assertEquals(699f, layout.buy.top, 0f)
        assertEquals(309f, layout.buy.right, 0f)
        assertEquals(744f, layout.buy.bottom, 0f)
        assertEquals(8f, layout.bottomConsole.left, 0f)
        assertEquals(735f, layout.bottomConsole.top, 0f)
        assertEquals(382f, layout.bottomConsole.right, 0f)
        assertEquals(839f, layout.bottomConsole.bottom, 0f)
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
}

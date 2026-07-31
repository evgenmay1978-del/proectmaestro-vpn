package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.semantics.Role
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

/**
 * ⚠️ Ни один workflow сейчас не вызывает `connectedAndroidTest`/`assembleAndroidTest`
 * (CLAUDE.md, раздел «Сборка и релиз»), поэтому этот файл документирует контракт деки и
 * ловит регрессии только на устройстве. Порядок и подписи протоколов продублированы
 * JVM-тестом `PhoneHomeProtocolOrderTest`, который в CI действительно исполняется.
 */
class PhoneHomeControlDeckTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val calls = mutableListOf<String>()

    @Test
    fun selectedReferencePrimaryActionsRemainReachable() {
        composeRule.setContent { TestDeck() }

        composeRule.onNodeWithTag("home-action-buy").performClick()
        composeRule.onNodeWithTag("home-action-login").performClick()
        composeRule.onNodeWithTag("home-action-network-test").assertHasClickAction()
        composeRule.onNodeWithTag("home-action-share").performClick()

        assertEquals(listOf("buy", "login", "share"), calls)
    }

    @Test
    fun protocolsExposeRadioSemanticsWithoutOpaqueSelectionCard() {
        composeRule.setContent { TestDeck() }

        composeRule.onNodeWithTag("home-protocol-vless")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton))
            .assertIsSelected()
            .performClick()
        composeRule.onNodeWithTag("home-protocol-olcrtc")
            .assertIsNotSelected()
            .performClick()

        assertEquals(listOf("protocol:vless", "olcrtc-request"), calls)
    }

    @Test
    fun wdttKeepsItsOwnSectorOnThePhone() {
        composeRule.setContent { TestDeck() }

        composeRule.onNodeWithTag("home-protocol-vk-turn")
            .assertIsNotSelected()
            .performClick()

        assertEquals(listOf("protocol:vk-turn"), calls)
    }

    @Test
    fun supportContactsKeepTheirOwnTargets() {
        composeRule.setContent { TestDeck() }

        composeRule.onNodeWithTag("home-action-phone").assertHasClickAction()
        composeRule.onNodeWithTag("home-contact-telegram").assertHasClickAction()
        composeRule.onNodeWithTag("home-contact-max").assertHasClickAction()
        composeRule.onNodeWithTag("home-contact-whatsapp").assertHasClickAction()
        composeRule.onNodeWithTag("home-support-note").assertExists()
    }

    @Test
    fun sixScreenFlowStaysReachableBelowTheReferenceFold() {
        composeRule.setContent { TestDeck(hasSubProfile = false) }

        composeRule.onNodeWithTag("home-action-trial").performScrollTo().performClick()
        composeRule.onNodeWithTag("home-action-scan-qr").performScrollTo().performClick()
        composeRule.onNodeWithTag("home-action-split").performScrollTo().performClick()

        assertEquals(listOf("trial", "scanqr", "split"), calls)
    }

    @Test
    fun accountCardSurvivesTheRebuildBelowTheFold() {
        composeRule.setContent { TestDeck() }

        composeRule.onNodeWithTag("premium-account").performScrollTo().assertExists()
    }

    @androidx.compose.runtime.Composable
    private fun TestDeck(hasSubProfile: Boolean = true) {
        PhoneHomeControlDeck(
            statusText = "Подключено",
            connected = true,
            connecting = false,
            activeProtocol = "vless",
            accountLogin = "demo",
            daysLeft = 30,
            accountExpires = null,
            protocols = listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn"),
            selected = "vless",
            hasSubProfile = hasSubProfile,
            hasOlcrtcCreds = false,
            olcrtcProvider = null,
            onSelectProtocol = { calls += "protocol:$it" },
            onSelectOlcrtc = { calls += "olcrtc-request" },
            onBuy = { calls += "buy" },
            onEnterCode = { calls += "login" },
            onSplitTunnel = { calls += "split" },
            onShareIos = { calls += "share" },
            onScanQr = { calls += "scanqr" },
            onEnterTrial = { calls += "trial" },
        )
    }
}

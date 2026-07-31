package com.maestrovpn.tv.compose.premium

import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class MobilePremiumShellTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun shellKeepsBackAndContentAccessible() {
        var backClicked = false
        composeRule.setContent {
            MobilePremium4DShell(
                title = "Activation",
                onBack = { backClicked = true },
                backContentDescription = "Back",
            ) {
                Text("Activation content")
            }
        }

        composeRule.onNodeWithTag("premium-mobile-shell").assertExists()
        composeRule.onNodeWithTag("premium-mobile-shell-background").assertExists()
        composeRule.onNodeWithText("Activation").assertExists()
        composeRule.onNodeWithText("Activation content").assertExists()
        composeRule.onNodeWithContentDescription("Back")
            .assertHasClickAction()
            .performClick()

        assertTrue(backClicked)
    }

    @Test
    fun dialogAndSheetSurfacesKeepTheirContent() {
        composeRule.setContent {
            MobilePremiumDialogSurface(title = "Permission") {
                Text("Camera is required")
            }
            MobilePremiumSheetSurface(modifier = Modifier) {
                Text("Scanner")
            }
        }

        composeRule.onNodeWithTag("premium-dialog-surface").assertExists()
        composeRule.onNodeWithTag("premium-sheet-surface").assertExists()
        composeRule.onNodeWithText("Permission").assertExists()
        composeRule.onNodeWithText("Camera is required").assertExists()
        composeRule.onNodeWithText("Scanner").assertExists()
    }
}

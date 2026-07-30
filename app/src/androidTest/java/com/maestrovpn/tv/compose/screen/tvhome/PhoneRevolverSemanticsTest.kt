package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.layout.Column
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Public
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

class PhoneRevolverSemanticsTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun protocolTilesExposeRadioSelectionWhileLockedRequestStaysUnselected() {
        var selectedClicks = 0
        var lockedRequests = 0
        composeRule.setContent {
            Column {
                PremiumProtocolTile(
                    label = "VLESS",
                    subtitle = "Selected protocol",
                    icon = Icons.Filled.Public,
                    selected = true,
                    locked = false,
                    onClick = { selectedClicks += 1 },
                )
                PremiumProtocolTile(
                    label = "olcRTC",
                    subtitle = "Request access",
                    icon = Icons.Filled.Lock,
                    selected = false,
                    locked = true,
                    onClick = { lockedRequests += 1 },
                )
            }
        }

        composeRule.onNodeWithText("VLESS")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton))
            .assertIsSelected()
            .performClick()
        composeRule.onNodeWithText("olcRTC")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton))
            .assertIsNotSelected()
            .performClick()

        assertEquals(1, selectedClicks)
        assertEquals(1, lockedRequests)
    }

    @Test
    fun actionAndContactTileComponentExposesButtonRoleAndCallback() {
        var clicks = 0
        composeRule.setContent {
            PremiumMenuActionTile(
                label = "Contact support",
                icon = Icons.Filled.Public,
                onClick = { clicks += 1 },
            )
        }

        composeRule.onNodeWithText("Contact support")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .performClick()

        assertEquals(1, clicks)
    }
}

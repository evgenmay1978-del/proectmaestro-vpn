package com.maestrovpn.tv.compose.premium

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertExists
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.fetchSemanticsNode
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class MobilePremiumControlsTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun premiumButtonExposesLabelClickAndMinimumHeight() {
        var clicked = false
        composeRule.setContent {
            MobilePremiumButton(label = "Повторить", onClick = { clicked = true })
        }

        val node = composeRule.onNodeWithText("Повторить")
            .assertExists()
            .assertHasClickAction()
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
        node.performClick()

        assertTrue(clicked)
        val heightPx = node.fetchSemanticsNode().boundsInRoot.height
        val minPx = with(composeRule.density) { 48.dp.toPx() }
        assertTrue(heightPx >= minPx)
    }

    @Test
    fun premiumErrorExposesMessageAndRetry() {
        composeRule.setContent {
            MobilePremiumError(
                message = "не удалось загрузить тарифы",
                onRetry = {},
                retryLabel = "Повторить",
            )
        }

        composeRule.onNodeWithText("не удалось загрузить тарифы").assertExists()
        composeRule.onNodeWithText("Повторить").assertHasClickAction()
    }

    @Test
    fun disabledButtonExposesDisabledState() {
        composeRule.setContent {
            MobilePremiumButton(
                label = "Недоступно",
                onClick = {},
                enabled = false,
            )
        }

        composeRule.onNodeWithText("Недоступно")
            .assertIsNotEnabled()
    }

    @Test
    fun populatedTextFieldKeepsItsPurposeInSemantics() {
        composeRule.setContent {
            MobilePremiumTextField(
                value = "known-account",
                onValueChange = {},
                placeholder = "Subscription login",
            )
        }

        composeRule.onNodeWithContentDescription("Subscription login")
            .assert(
                SemanticsMatcher.expectValue(
                    SemanticsProperties.EditableText,
                    AnnotatedString("known-account"),
                ),
            )
    }

    @Test
    fun segmentedOptionsExposeRadioRoleAndSelection() {
        var selectedIndex = 0
        composeRule.setContent {
            MobilePremiumSegmented(
                options = listOf("Месяц", "Год"),
                selectedIndex = selectedIndex,
                onSelect = { selectedIndex = it },
            )
        }

        composeRule.onNodeWithText("Год")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton))
            .performClick()

        assertTrue(selectedIndex == 1)
    }

    @Test
    fun appRowExposesCheckboxRoleAndSelection() {
        var selected = false
        composeRule.setContent {
            MobilePremiumAppRow(
                label = "Браузер",
                supportingText = null,
                icon = null,
                selected = selected,
                onSelectedChange = { selected = it },
                modifier = androidx.compose.ui.Modifier,
            )
        }

        composeRule.onNodeWithText("Браузер")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Checkbox))
            .assertIsEnabled()
            .assertHasClickAction()
            .performClick()

        assertTrue(selected)
    }
}

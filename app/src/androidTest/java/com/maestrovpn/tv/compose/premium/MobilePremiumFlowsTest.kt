package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.screen.claim.ClaimPhoneForm
import com.maestrovpn.tv.compose.screen.claim.ClaimState
import com.maestrovpn.tv.compose.screen.purchase.BuyState
import com.maestrovpn.tv.compose.screen.purchase.PhonePaymentContent
import com.maestrovpn.tv.compose.screen.purchase.PhonePaymentResultContent
import com.maestrovpn.tv.compose.screen.purchase.PhoneTariffSelection
import com.maestrovpn.tv.compose.screen.purchase.TariffItem
import com.maestrovpn.tv.compose.screen.trial.TrialPhoneForm
import com.maestrovpn.tv.compose.screen.trial.TrialState
import com.maestrovpn.tv.compose.screen.tvhome.TvHomeScreen
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class MobilePremiumFlowsTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun phoneHomeShowsPremiumStatusProtocolAndRevolver() {
        composeRule.setContent {
            TvHomeScreen(
                statusText = "Подключено",
                connected = true,
                protocols = listOf("auto", "vless", "hysteria2"),
                selected = "auto",
                activeProtocol = "vless",
                accountLogin = "demo",
                daysLeft = 30,
                accountExpires = null,
                hasSubProfile = true,
                onToggleConnect = {},
                onSelectProtocol = {},
                onBuy = {},
                onEnterCode = {},
            )
        }

        composeRule.onNodeWithTag("premium-phone-home").assertExists()
        composeRule.onNodeWithText("ПОДКЛЮЧЕНО", substring = true).assertExists()
        composeRule.onNodeWithText("VLESS").assertExists()
        composeRule.onNodeWithTag("premium-revolver").assertExists()
    }

    @Test
    fun phoneTariffsShowItemsInOrderAndBuyTheSelectedKey() {
        var boughtKey: String? = null
        composeRule.setContent {
            PhoneTariffSelection(
                items = listOf(
                    TariffItem(key = "month", name = "One month", rub = 149),
                    TariffItem(key = "year", name = "One year", rub = 999),
                ),
                onBuy = { boughtKey = it },
            )
        }

        composeRule.onNodeWithTag("premium-tariffs").assertExists()
        composeRule.onNodeWithText("One month").assertExists()
        composeRule.onNodeWithText("149 ₽").assertExists()
        composeRule.onNodeWithText("One year").assertExists()
        composeRule.onNodeWithText("999 ₽").assertExists()
        composeRule.onNodeWithText("One year").performClick()

        assertEquals("year", boughtKey)
    }

    @Test
    fun phonePaymentShowsDetailsInOrderAndInvokesPaymentActions() {
        var openedPaymentUrl: String? = null
        var paid = false
        val payment = BuyState.AwaitingPayment(
            rub = 349,
            code = "ORDER-MOBILE-42",
            phone = "+7 999 123-45-67",
            payUrl = "https://pay.example/order-mobile-42",
        )

        composeRule.setContent {
            Box(
                modifier = Modifier
                    .width(320.dp)
                    .padding(horizontal = 18.dp),
            ) {
                PhonePaymentContent(
                    state = payment,
                    onOpenPayment = { openedPaymentUrl = it },
                    onPaid = { paid = true },
                )
            }
        }

        val panel = composeRule.onNodeWithTag("premium-payment")
            .assertExists()
            .fetchSemanticsNode()
        val amount = composeRule.onNodeWithText("Сумма: 349 ₽").fetchSemanticsNode()
        val qr = composeRule.onNodeWithContentDescription("QR для оплаты").fetchSemanticsNode()
        val instructions = composeRule
            .onNodeWithText("Отсканируйте телефоном — откроется оплата", substring = true)
            .fetchSemanticsNode()
        val orderCode = composeRule.onNodeWithText("ORDER-MOBILE-42").fetchSemanticsNode()
        val manualPhone = composeRule
            .onNodeWithText("Или вручную по СБП на номер: +7 999 123-45-67")
            .fetchSemanticsNode()
        val paidButton = composeRule.onNodeWithText("Я оплатил").fetchSemanticsNode()

        assertTrue(amount.boundsInRoot.top < qr.boundsInRoot.top)
        assertTrue(qr.boundsInRoot.top < instructions.boundsInRoot.top)
        assertTrue(instructions.boundsInRoot.top < orderCode.boundsInRoot.top)
        assertTrue(orderCode.boundsInRoot.top < manualPhone.boundsInRoot.top)
        assertTrue(manualPhone.boundsInRoot.top < paidButton.boundsInRoot.top)
        assertEquals(qr.boundsInRoot.width, qr.boundsInRoot.height, 0.5f)
        assertTrue(qr.boundsInRoot.left >= panel.boundsInRoot.left)
        assertTrue(qr.boundsInRoot.right <= panel.boundsInRoot.right)

        composeRule.onNodeWithText("Открыть страницу оплаты").performClick()
        composeRule.onNodeWithText("Я оплатил").performClick()

        assertEquals("https://pay.example/order-mobile-42", openedPaymentUrl)
        assertTrue(paid)
    }

    @Test
    fun phonePaymentShowsWaitingActivatingDoneAndRetryStates() {
        var state by mutableStateOf<BuyState>(BuyState.AwaitingConfirm)
        var retries = 0
        composeRule.setContent {
            PhonePaymentResultContent(
                state = state,
                onRetry = { retries += 1 },
            )
        }

        composeRule.onNodeWithTag("premium-payment").assertExists()
        composeRule.onNodeWithText("Ожидаем подтверждение оплаты…").assertExists()

        composeRule.runOnUiThread {
            state = BuyState.Activating
        }
        composeRule.onNodeWithText("Активируем подписку…").assertExists()

        composeRule.runOnUiThread {
            state = BuyState.Done
        }
        composeRule.onNodeWithText("Готово!").assertExists()

        composeRule.runOnUiThread {
            state = BuyState.Error("платёж отклонён")
        }
        composeRule.onNodeWithText("Ошибка: платёж отклонён").assertExists()
        composeRule.onNodeWithText("Повторить").performClick()

        assertEquals(1, retries)
    }

    @Test
    fun phoneClaimShowsActivationControls() {
        composeRule.setContent {
            ClaimPhoneForm(
                code = "",
                onCodeChange = {},
                state = ClaimState.Idle,
                onClaim = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("premium-claim").assertExists()
        composeRule.onNodeWithText("Активация подписки").assertExists()
        composeRule.onNodeWithText("Код или логин").assertExists()
        composeRule.onNodeWithText("Активировать").assertExists()
    }

    @Test
    fun phoneClaimShowsBusyAndErrorStates() {
        var state by mutableStateOf<ClaimState>(ClaimState.Busy)
        composeRule.setContent {
            ClaimPhoneForm(
                code = "claim-code",
                onCodeChange = {},
                state = state,
                onClaim = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithText("Проверяем…").assertIsNotEnabled()

        composeRule.runOnUiThread {
            state = ClaimState.Error("Код не найден")
        }

        composeRule.onNodeWithText("Код не найден").assertExists()
        composeRule.onNodeWithText("Повторить").assertExists()
    }

    @Test
    fun phoneTrialShowsActivationControls() {
        composeRule.setContent {
            TrialPhoneForm(
                nick = "",
                onNickChange = {},
                state = TrialState.Idle,
                onActivate = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithTag("premium-trial").assertExists()
        composeRule.onNodeWithText("Бесплатный пробный период").assertExists()
        composeRule.onNodeWithText("Ваш ник").assertExists()
        composeRule.onNodeWithText("Получить 2 дня").assertExists()
    }

    @Test
    fun phoneTrialShowsBusyAndErrorStates() {
        var state by mutableStateOf<TrialState>(TrialState.Busy)
        composeRule.setContent {
            TrialPhoneForm(
                nick = "demo",
                onNickChange = {},
                state = state,
                onActivate = {},
                onBack = {},
            )
        }

        composeRule.onNodeWithText("Активируем…").assertIsNotEnabled()

        composeRule.runOnUiThread {
            state = TrialState.Error("Ник уже занят")
        }

        composeRule.onNodeWithText("Ник уже занят").assertExists()
        composeRule.onNodeWithText("Повторить").assertExists()
    }
}

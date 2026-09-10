package com.maestrovpn.tv.compose

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.maestrovpn.tv.compose.premium.MobilePremium4DShell
import com.maestrovpn.tv.compose.screen.claim.ClaimPhoneForm
import com.maestrovpn.tv.compose.screen.claim.ClaimState
import com.maestrovpn.tv.compose.screen.purchase.BuyState
import com.maestrovpn.tv.compose.screen.purchase.PhonePaymentContent
import com.maestrovpn.tv.compose.screen.purchase.PhonePaymentResultContent
import com.maestrovpn.tv.compose.screen.tvhome.PhoneDashboard
import com.maestrovpn.tv.compose.theme.SFATheme
import com.maestrovpn.tv.database.ProfileManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/** Real phone components on the disposable CI emulator; no claim, payment or VPN backend. */
@RunWith(AndroidJUnit4::class)
class PhoneComponentGeometryInstrumentedTest {
    @get:Rule val ui = createComposeRule()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext

    @Before fun requireFixtureEnvironment() {
        assertEquals("Phone fixture density must be 450 dpi", 450, context.resources.configuration.densityDpi)
        assertTrue("Component fixtures require an empty disposable account store",
            runBlocking(Dispatchers.IO) { ProfileManager.list().isEmpty() })
        ui.mainClock.autoAdvance = false
    }

    @Test fun homeOffKeepsControlsReachable() {
        homeFixture(connected = false, connecting = false, status = "Отключено", name = "off")
    }

    @Test fun homeStartingKeepsControlsReachable() {
        homeFixture(connected = false, connecting = true, status = "Подключение…", name = "starting")
    }

    @Test fun homeOnKeepsControlsReachable() {
        homeFixture(connected = true, connecting = false, status = "Подключено", name = "on")
    }

    @Test fun changingLoginRetainsInitialNameAndRequiresNonBlankInput() {
        val login = mutableStateOf("fixture-old-login")
        var claimRequests = 0
        ui.setContent {
            SFATheme {
                ClaimPhoneForm(
                    code = login.value,
                    onCodeChange = { login.value = it },
                    state = ClaimState.Idle,
                    onClaim = { claimRequests++ },
                    onBack = {},
                    changingLogin = true,
                )
            }
        }
        settle()
        ui.onNodeWithText("Сменить логин").assertIsDisplayed()
        val field = ui.onNode(hasSetTextAction())
        field.assertIsDisplayed().assertTextEquals("fixture-old-login")
        field.performTextReplacement("")
        settleFrame()
        scrollTo("Войти").assertIsNotEnabled()
        field.performTextReplacement("fixture-new-login")
        settleFrame()
        field.assertTextEquals("fixture-new-login")
        scrollTo("Войти").assertIsEnabled()
        assertEquals(0, claimRequests)
        shot("fixture-login-change")
    }

    @Test fun awaitingPaymentUsesOnlyFixtureCallbacks() {
        val invoice = BuyState.AwaitingPayment(
            rub = 300, code = "FIXTURE-ORDER", phone = "",
            payUrl = "https://payments.invalid/fixture-order",
        )
        var openedUrl: String? = null
        var paidClicks = 0
        paymentFixture {
            PhonePaymentContent(invoice, onOpenPayment = { openedUrl = it }, onPaid = { paidClicks++ },
                modifier = Modifier.fillMaxWidth())
        }
        ui.onNodeWithContentDescription("QR для оплаты").assertIsDisplayed()
        shot("fixture-payment-awaiting")
        scrollTo("Открыть страницу оплаты").assertIsEnabled().performClick()
        scrollTo("Я оплатил").assertIsEnabled().performClick()
        ui.runOnIdle {
            assertEquals(invoice.payUrl, openedUrl)
            assertEquals(1, paidClicks)
        }
    }

    @Test fun paymentConfirmationAndErrorRemainVisibleAndRetryWorks() {
        val state = mutableStateOf<BuyState>(BuyState.AwaitingConfirm)
        var retryClicks = 0
        paymentFixture {
            PhonePaymentResultContent(state.value, onRetry = { retryClicks++ }, modifier = Modifier.fillMaxWidth())
        }
        ui.onNodeWithText("Ожидаем подтверждение оплаты…").assertIsDisplayed()
        shot("fixture-payment-confirmation")
        ui.runOnIdle { state.value = BuyState.Error("fixture-confirmation-unavailable") }
        settleFrame()
        ui.onNodeWithText("Ошибка: fixture-confirmation-unavailable").assertIsDisplayed()
        scrollTo("Повторить").assertIsEnabled().performClick()
        ui.runOnIdle { assertEquals(1, retryClicks) }
        shot("fixture-payment-error")
    }

    private fun homeFixture(connected: Boolean, connecting: Boolean, status: String, name: String) {
        var renewClicks = 0
        ui.setContent {
            SFATheme {
                PhoneDashboard(
                    connected = connected,
                    connecting = connecting,
                    protocols = listOf("auto", "vless"),
                    selected = "vless",
                    activeProtocol = if (connected) "vless" else null,
                    accountLogin = "fixture-login",
                    daysLeft = 29,
                    accountExpires = "2027-01-01",
                    hasSubProfile = true,
                    onToggleConnect = {},
                    onSelectProtocol = {},
                    onBuy = { renewClicks++ },
                    onEnterCode = {},
                    onOpenServers = {},
                    onOpenSettings = {},
                    onSplitTunnel = {},
                    onShareIos = {},
                    onScanQr = {},
                    onEnterTrial = {},
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        // One second completes connection opening (430 ms), before the earliest idle blink.
        // OFF remains genuinely closed and STARTING uses PhoneDashboard's real half-open state.
        settle()
        ui.onNodeWithText(status, useUnmergedTree = true).assertIsDisplayed()
        ui.onNodeWithContentDescription(if (connected && !connecting) "Отключить VPN" else "Подключить VPN")
            .assertIsDisplayed().assertHasClickAction()
        for (label in listOf("fixture-login", "Бот", "Обычный VPN", "Главная", "Серверы", "Подписка", "Настройки")) {
            ui.onNodeWithText(label).assertIsDisplayed().assertHasClickAction()
        }
        shot("fixture-home-$name")
        scrollTo("Продлить VPN").assertIsEnabled().performClick()
        scrollTo("Купить ГБ").assertIsEnabled().assertHasClickAction()
        ui.runOnIdle { assertEquals(1, renewClicks) }
    }

    private fun paymentFixture(content: @Composable () -> Unit) {
        ui.setContent {
            SFATheme {
                MobilePremium4DShell(title = "Подписка", onBack = {}) {
                    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(vertical = 12.dp),
                        horizontalAlignment = Alignment.CenterHorizontally) {
                        content()
                    }
                }
            }
        }
        settle()
    }

    private fun settle() {
        ui.mainClock.advanceTimeBy(1_000L)
        ui.waitForIdle()
    }

    private fun settleFrame() {
        ui.mainClock.advanceTimeByFrame()
        ui.waitForIdle()
    }

    private fun scrollTo(label: String): SemanticsNodeInteraction {
        val node = ui.onNodeWithText(label)
        node.performScrollTo()
        settleFrame()
        return node.assertIsDisplayed()
    }

    private fun shot(name: String) {
        ui.waitForIdle()
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot()) { "No screenshot for $name" }
        try {
            assertEquals("Fixture viewport width", 1080, bitmap.width)
            assertEquals("Fixture viewport height", 2340, bitmap.height)
            val directory = requireNotNull(context.getExternalFilesDir("ui-captures"))
            assertTrue(directory.isDirectory || directory.mkdirs())
            File(directory, "$name.png").outputStream().use {
                assertTrue("Could not save $name", bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        } finally {
            bitmap.recycle()
        }
    }
}

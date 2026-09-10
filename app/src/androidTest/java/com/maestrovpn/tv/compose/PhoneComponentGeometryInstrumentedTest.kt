package com.maestrovpn.tv.compose

import android.graphics.Bitmap
import android.os.Build
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import androidx.activity.ComponentActivity
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
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
import com.maestrovpn.tv.compose.screen.tvhome.TvEskizHome
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
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/** Real phone components on the disposable CI emulator; no claim, payment or VPN backend. */
@RunWith(AndroidJUnit4::class)
class PhoneComponentGeometryInstrumentedTest {
    @get:Rule val ui = createAndroidComposeRule<ComponentActivity>()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext
    private val tvPreview get() = InstrumentationRegistry.getArguments().getString("preview_form_factor") == "tv"

    @Before fun requireFixtureEnvironment() {
        assertEquals("Fixture density", if (tvPreview) 160 else 450, context.resources.configuration.densityDpi)
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

    /** Full native Home, including real OFF -> ON -> OFF transitions; no backend is called. */
    fun homeEyeMotionPreview() {
        assertTrue("Eye motion preview requires the phone viewport", !tvPreview)
        val connected = mutableStateOf(false)
        ui.setContent { SFATheme { homeContent(connected.value, false) {} } }
        settle()
        ui.onNodeWithText("Отключено", useUnmergedTree = true).assertIsDisplayed()
        shot("fixture-eye-home-off")

        // Start only after shot() has observed window focus and a committed native frame.
        // Keep the same visible Activity until screenrecord closes the MP4 itself.
        val directory = requireNotNull(context.getExternalFilesDir("ui-captures"))
        val video = File(directory, "fixture-eye-home-motion.mp4")
        val recording = instrumentation.uiAutomation.executeShellCommand(
            "screenrecord --time-limit 26 --bit-rate 6000000 --size 1080x2340 '${video.absolutePath}'",
        )
        val recordingLog = ByteArrayOutputStream()
        val recordingFinished = CountDownLatch(1)
        thread(name = "native-eye-preview-recording") {
            try {
                ParcelFileDescriptor.AutoCloseInputStream(recording).use { it.copyTo(recordingLog) }
            } finally {
                recordingFinished.countDown()
            }
        }
        val started = SystemClock.uptimeMillis()
        val clockStarted = ui.mainClock.currentTime
        var opened = false
        var capturedOn = false
        var closed = false
        while (SystemClock.uptimeMillis() - started < 27_000L) {
            val elapsed = SystemClock.uptimeMillis() - started
            if (elapsed >= 2_000L && !opened) {
                ui.runOnUiThread { connected.value = true }
                opened = true
            }
            if (elapsed >= 22_000L && !closed) {
                ui.runOnUiThread { connected.value = false }
                closed = true
            }
            // Test clocks do not advance by sleeping. Follow wall time explicitly so the
            // native recording shows motion at its intended speed, not a frozen fixture.
            val due = elapsed - (ui.mainClock.currentTime - clockStarted)
            if (due > 0L) ui.mainClock.advanceTimeBy(due)
            if (elapsed >= 3_500L && !capturedOn) {
                ui.onNodeWithText("Подключено", useUnmergedTree = true).assertIsDisplayed()
                shot("fixture-eye-home-on")
                capturedOn = true
            }
            SystemClock.sleep(16L)
        }
        assertTrue("screenrecord must finish within its bounded deadline", recordingFinished.await(5, TimeUnit.SECONDS))
        File(directory, "fixture-eye-recording.txt").writeBytes(recordingLog.toByteArray())
        assertTrue("Native eye video is missing or empty", video.isFile && video.length() > 0L)
        ui.onNodeWithText("Отключено", useUnmergedTree = true).assertIsDisplayed()
        shot("fixture-eye-home-off-after")
    }

    /** The production TV component on a landscape native surface, not TV hardware validation. */
    fun tvHomeLayoutPreview() {
        assertTrue("TV layout preview requires its landscape fixture viewport", tvPreview)
        ui.setContent {
            SFATheme {
                TvEskizHome(
                    statusText = "Отключено", connected = false,
                    protocols = listOf("auto", "vless"), selected = "vless", activeProtocol = null,
                    accountLogin = "fixture-login", daysLeft = 29, accountExpires = "2027-01-01",
                    hasSubProfile = true, hasOlcrtcCreds = false, olcrtcProvider = null,
                    onToggleConnect = {}, onSelectProtocol = {}, onSelectOlcrtc = {},
                    onBuy = {}, onEnterCode = {}, onSplitTunnel = {}, onShareIos = {}, onEnterTrial = {},
                    connectFocus = FocusRequester(),
                )
            }
        }
        settle()
        ui.onNodeWithText("MaestroVPN").assertIsDisplayed()
        ui.onNodeWithText("fixture-login").assertIsDisplayed()
        shot("fixture-tv-home")
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
                homeContent(connected, connecting) { renewClicks++ }
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

    @Composable private fun homeContent(connected: Boolean, connecting: Boolean, onBuy: () -> Unit) {
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
            onBuy = onBuy,
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

    private fun paymentFixture(content: @Composable () -> Unit) {
        // Payment has no blink phase to freeze; let its real rendering clock advance.
        ui.mainClock.autoAdvance = true
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
        ui.waitUntil(5_000) { ui.runOnUiThread { ui.activity.window.decorView.hasWindowFocus() } }
        val committed = CountDownLatch(1)
        ui.runOnUiThread {
            val view = ui.activity.window.decorView
            assertTrue("The native fixture window must use hardware rendering", view.isHardwareAccelerated)
            if (Build.VERSION.SDK_INT >= 29) {
                view.viewTreeObserver.registerFrameCommitCallback { committed.countDown() }
                view.invalidate()
            } else error("Native fixture frame capture requires Android 29+")
        }
        assertTrue("The fixture frame must be committed before capture", committed.await(5, TimeUnit.SECONDS))
        SystemClock.sleep(250)
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot()) { "No screenshot for $name" }
        try {
            assertEquals("Fixture viewport width", if (tvPreview) 1920 else 1080, bitmap.width)
            assertEquals("Fixture viewport height", if (tvPreview) 1080 else 2340, bitmap.height)
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

/** Opt-in capture methods kept separate so the existing component suite stays unchanged. */
@RunWith(AndroidJUnit4::class)
class PhoneEyePreviewInstrumentedTest {
    private val fixture = PhoneComponentGeometryInstrumentedTest()
    @get:Rule val ui = fixture.ui

    @Before fun requireFixtureEnvironment() = fixture.requireFixtureEnvironment()
    @Test fun homeEyeMotionPreview() = fixture.homeEyeMotionPreview()
    @Test fun tvHomeLayoutPreview() = fixture.tvHomeLayoutPreview()
}

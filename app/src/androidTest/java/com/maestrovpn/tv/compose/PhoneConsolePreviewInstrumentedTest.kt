package com.maestrovpn.tv.compose

import android.graphics.Bitmap
import android.os.Build
import android.os.SystemClock
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.maestrovpn.tv.compose.screen.tvhome.PhoneConsoleHome
import com.maestrovpn.tv.compose.screen.tvhome.PhoneConsoleLayout
import com.maestrovpn.tv.compose.screen.tvhome.PhoneServer
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
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** Production phone components on a disposable native surface; no account, purchase or VPN calls. */
@RunWith(AndroidJUnit4::class)
class PhoneConsolePreviewInstrumentedTest {
    @get:Rule val ui = createAndroidComposeRule<ComponentActivity>()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext
    private val arguments get() = InstrumentationRegistry.getArguments()
    private val profile get() = requireNotNull(arguments.getString("profile"))
    private val expectedWidthDp get() = requireNotNull(arguments.getString("expectedWidthDp")).toInt()
    private val expectedHeightDp get() = requireNotNull(arguments.getString("expectedHeightDp")).toInt()
    private val expectedFontScale get() = requireNotNull(arguments.getString("fontScale")).toFloat()
    private val calls = mutableMapOf<String, Int>()

    @Before fun requireDisposableNativeWindow() {
        assertTrue("Profile must be a safe capture filename", profile.matches(Regex("[a-z0-9-]+")))
        assertEquals("Native preview density", 480, context.resources.configuration.densityDpi)
        assertEquals("System font scale", expectedFontScale, context.resources.configuration.fontScale, 0.01f)
        assertTrue("The UI fixture must not use an owner account",
            runBlocking(Dispatchers.IO) { ProfileManager.list().isEmpty() })
        val bounds = ui.activity.windowManager.currentWindowMetrics.bounds
        assertEquals("Native window width", expectedWidthDp * 3, bounds.width())
        assertEquals("Native window height", expectedHeightDp * 3, bounds.height())
        ui.mainClock.autoAdvance = false
    }

    @Test fun consoleStatesAndActionsStayReachable() {
        val cases = listOf(
            Preview(false, false, "Отключено", "fixture-login", "Безлимит", "19,16 ГБ",
                PhoneServer("Автовыбор сервера", "Обычный VPN"), false, "off"),
            Preview(false, true, "Подключение…", "fixture-long-login-для-проверки-кнопки-редактирования",
                "365 дней", "0,01 ГБ", PhoneServer("Очень длинное название сервера Нидерланды — основной выход",
                    "CDN · мобильная сеть"), true, "connecting"),
            Preview(true, false, "Подключено", "fixture-long-login-для-проверки-кнопки-редактирования",
                "Безлимит", "9 999,99 ГБ", PhoneServer("Германия — длинное название выбранного сервера",
                    "CDN · мобильная сеть"), true, "on"),
            Preview(false, false, "Не удалось подключиться. Проверьте сеть и повторите попытку.",
                "fixture-login", "Подписка истекла", "0 ГБ",
                PhoneServer("Нидерланды — резервный сервер с длинным названием", "CDN · мобильная сеть"), true, "error"),
        )
        val current = mutableStateOf(cases.first())
        ui.setContent {
            SFATheme {
                PhoneConsoleLayout(
                    activeTab = "home", onHome = { record("home") }, onServers = { record("nav-servers") },
                    onAccount = { record("nav-account") }, onSettings = { record("settings") },
                    modifier = Modifier.fillMaxSize(),
                ) { heroWidth ->
                    val state = current.value
                    PhoneConsoleHome(
                        connected = state.connected, connecting = state.connecting, statusText = state.status,
                        accountLabel = state.login, hasSubProfile = true, daysText = state.days,
                        balanceText = state.balance, server = state.server, cdnSelected = state.cdn,
                        heroWidth = heroWidth, onToggleConnect = { record("connect") },
                        onEnterCode = { record("login") }, onBot = { record("bot") },
                        onAccount = { record("wallet-vpn") }, onCdn = { record("cdn") },
                        onOrdinary = { record("ordinary") }, onServers = { record("server") },
                        onBuy = { record("buy-vpn") }, onBuyCdn = { record("buy-cdn") },
                    )
                }
            }
        }
        for ((index, state) in cases.withIndex()) {
            ui.runOnUiThread { current.value = state }
            settle()
            scrollToTop()
            val account = ui.onNodeWithTag("phone-console-account").assertIsDisplayed()
            val bot = ui.onNodeWithTag("phone-console-bot").assertIsDisplayed()
            assertEquals("Account and bot must have equal height", account.getUnclippedBoundsInRoot().height,
                bot.getUnclippedBoundsInRoot().height, 1f)
            val vpn = ui.onNodeWithTag("phone-console-vpn-wallet").assertIsDisplayed()
            val cdn = ui.onNodeWithTag("phone-console-cdn-wallet").assertIsDisplayed()
            assertEquals("Wallets must have equal height", vpn.getUnclippedBoundsInRoot().height,
                cdn.getUnclippedBoundsInRoot().height, 1f)
            assertEquals("The upper groups share their left edge", account.getUnclippedBoundsInRoot().left,
                vpn.getUnclippedBoundsInRoot().left, 1f)
            assertEquals("The upper groups share their right edge", bot.getUnclippedBoundsInRoot().right,
                cdn.getUnclippedBoundsInRoot().right, 1f)
            val medallion = show("phone-console-medallion")
            val circle = medallion.getUnclippedBoundsInRoot()
            val stateLabel = if (state.name == "error") "Ошибка подключения" else state.status
            medallion.assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, stateLabel))
            assertEquals("The medallion must remain circular", circle.width, circle.height, 1f)
            assertEquals("The medallion shares the screen axis", expectedWidthDp / 2f,
                circle.left + circle.width / 2f, 1f)
            show("phone-console-status")
            ui.onNodeWithText(state.status, useUnmergedTree = true).assertIsDisplayed()
            val action = if (state.connecting) "Отменить подключение" else if (state.connected) "Отключить VPN" else "Подключить VPN"
            ui.onNodeWithContentDescription(action).assertHasClickAction().assertIsEnabled()
            shot("${state.name}-status")
            for (tag in listOf("phone-console-modes", "phone-console-server", "phone-console-buy-vpn", "phone-console-buy-cdn")) {
                val node = show(tag)
                if (tag != "phone-console-modes") assertHitArea(node, tag)
            }
            val renew = ui.onNodeWithTag("phone-console-buy-vpn")
            val buy = ui.onNodeWithTag("phone-console-buy-cdn")
            assertEquals("Purchase buttons have equal height", renew.getUnclippedBoundsInRoot().height,
                buy.getUnclippedBoundsInRoot().height, 1f)
            assertEquals("Purchase buttons have equal width", renew.getUnclippedBoundsInRoot().width,
                buy.getUnclippedBoundsInRoot().width, 1f)
            val nav = ui.onNodeWithTag("phone-console-navigation").assertIsDisplayed()
            assertTrue("The bottom navigation must not cover the purchase row",
                buy.getUnclippedBoundsInRoot().bottom <= nav.getUnclippedBoundsInRoot().top)
            assertFullBoundsVisible(renew, "Renew")
            assertFullBoundsVisible(buy, "Buy GB")
            val navWidths = listOf("home", "servers", "account", "settings").map { name ->
                val node = ui.onNodeWithTag("phone-nav-$name").assertIsDisplayed().assertHasClickAction()
                assertHitArea(node, name)
                node.getUnclippedBoundsInRoot().width
            }
            assertTrue("Navigation items share equal width", navWidths.max() - navWidths.min() <= 1f)
            if (index == 0) shot("off-controls")
            if (index == 0) verifyCallbacksWithoutAdvancingConnection()
        }
    }

    private fun verifyCallbacksWithoutAdvancingConnection() {
        for ((tag, name) in listOf(
            "phone-console-account" to "login", "phone-console-bot" to "bot",
            "phone-console-vpn-wallet" to "wallet-vpn", "phone-console-cdn-wallet" to "cdn",
            "phone-console-medallion" to "connect", "phone-console-server" to "server",
            "phone-console-buy-vpn" to "buy-vpn", "phone-console-buy-cdn" to "buy-cdn",
        )) {
            val node = show(tag).assertHasClickAction()
            assertHitArea(node, name)
            node.performClick()
            ui.runOnIdle { assertEquals("The existing $name callback is wired", 1, calls[name]) }
        }
        show("phone-console-modes")
        ui.onNode(hasText("Обычный VPN") and hasAnyAncestor(hasTestTag("phone-console-modes")))
            .assertHasClickAction().performClick()
        ui.onNode(hasText("CDN") and hasAnyAncestor(hasTestTag("phone-console-modes")))
            .assertHasClickAction().performClick()
        for ((tag, name) in listOf("home" to "home", "servers" to "nav-servers", "account" to "nav-account", "settings" to "settings")) {
            ui.onNodeWithTag("phone-nav-$tag").performClick()
            ui.runOnIdle { assertEquals("The $tag navigation action is wired", 1, calls[name]) }
        }
        ui.runOnIdle {
            assertEquals("Ordinary mode uses its callback", 1, calls["ordinary"])
            assertEquals("Both CDN entry points use their callback", 2, calls["cdn"])
        }
        show("phone-console-status")
        ui.onNodeWithText("Отключено", useUnmergedTree = true).assertIsDisplayed()
        ui.onNodeWithText("Подключено", useUnmergedTree = true).assertDoesNotExist()
    }

    private fun record(name: String) { calls[name] = (calls[name] ?: 0) + 1 }

    private fun settle() {
        ui.mainClock.advanceTimeBy(1_000L)
        ui.waitForIdle()
    }

    private fun scrollToTop() {
        ui.onNodeWithTag("phone-console-scroll").performSemanticsAction(SemanticsActions.ScrollBy) { it(0f, -100_000f) }
        settle()
    }

    private fun show(tag: String): SemanticsNodeInteraction {
        val node = ui.onNodeWithTag(tag)
        node.performScrollTo()
        settle()
        return node.assertIsDisplayed()
    }

    private fun assertHitArea(node: SemanticsNodeInteraction, label: String) {
        val bounds = node.getUnclippedBoundsInRoot()
        assertTrue("$label touch target width", bounds.width >= 48f)
        assertTrue("$label touch target height", bounds.height >= 48f)
    }

    private fun assertFullBoundsVisible(node: SemanticsNodeInteraction, label: String) {
        val full = node.getUnclippedBoundsInRoot()
        val visible = node.fetchSemanticsNode().boundsInRoot
        with(ui.density) {
            assertEquals("$label left is clipped", full.left.toPx(), visible.left, 1f)
            assertEquals("$label top is clipped", full.top.toPx(), visible.top, 1f)
            assertEquals("$label right is clipped", full.right.toPx(), visible.right, 1f)
            assertEquals("$label bottom is clipped", full.bottom.toPx(), visible.bottom, 1f)
        }
    }

    private fun shot(name: String) {
        ui.waitForIdle()
        ui.waitUntil(10_000) { ui.runOnUiThread { ui.activity.window.decorView.hasWindowFocus() } }
        val committed = CountDownLatch(1)
        ui.runOnUiThread {
            val view = ui.activity.window.decorView
            assertTrue("Native captures require hardware rendering", view.isHardwareAccelerated)
            check(Build.VERSION.SDK_INT >= 29)
            view.viewTreeObserver.registerFrameCommitCallback { committed.countDown() }
            view.invalidate()
        }
        assertTrue("The native frame must commit before capture", committed.await(5, TimeUnit.SECONDS))
        SystemClock.sleep(250)
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
        try {
            assertEquals("Capture width", expectedWidthDp * 3, bitmap.width)
            assertEquals("Capture height", expectedHeightDp * 3, bitmap.height)
            val directory = requireNotNull(context.getExternalFilesDir("ui-captures"))
            assertTrue(directory.isDirectory || directory.mkdirs())
            File(directory, "fixture-console-$profile-$name.png").outputStream().use {
                assertTrue("Save native capture", bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        } finally {
            bitmap.recycle()
        }
    }

    private data class Preview(
        val connected: Boolean, val connecting: Boolean, val status: String, val login: String,
        val days: String, val balance: String, val server: PhoneServer, val cdn: Boolean, val name: String,
    )
}

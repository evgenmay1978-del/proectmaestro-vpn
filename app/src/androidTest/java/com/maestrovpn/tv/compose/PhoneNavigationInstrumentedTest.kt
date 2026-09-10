package com.maestrovpn.tv.compose

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.LocaleManager
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import android.os.LocaleList
import android.os.SystemClock
import android.view.KeyEvent
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.maestrovpn.tv.constant.SettingsKey
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.TypedProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.ExternalResource
import org.junit.rules.RuleChain
import org.junit.rules.TestWatcher
import org.junit.runner.Description
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** Real navigation on the disposable emulator; no account registration or payment submission. */
@RunWith(AndroidJUnit4::class)
class PhoneNavigationInstrumentedTest {
    private val ui = createAndroidComposeRule<MainActivity>()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext
    private var oldSelection = -1L
    private var oldApps = emptySet<String>()
    private var oldMode = 0
    private var oldEnabled = false
    private var fixture: Profile? = null
    private var fixtureFile: File? = null

    // MainActivity offers both system prompts before setContent. @Before is too late:
    // the activity rule has already launched and those dialogs can own the focused window.
    private val startupPrompts = object : ExternalResource() {
        private var oldTilePrompt: Boolean? = null
        private var hadBatteryPrompt = false
        private var oldBatteryPrompt = false
        private var oldAccessibilityFlags = 0
        private var oldApplicationLocales: LocaleList? = null

        override fun before() {
            // The CI emulator is API 34: LocaleManager applies the app locale before any
            // AppCompat activity exists, without changing the emulator's system locale.
            if (Build.VERSION.SDK_INT >= 33) {
                val manager = context.getSystemService(LocaleManager::class.java)
                oldApplicationLocales = manager.applicationLocales
                instrumentation.runOnMainSync { manager.applicationLocales = LocaleList.forLanguageTags("ru") }
            }
            oldTilePrompt = Settings.dataStore.getBoolean(SettingsKey.QS_TILE_PROMPTED)
            Settings.qsTilePrompted = true
            val prefs = context.getSharedPreferences("maestro_prefs", Context.MODE_PRIVATE)
            hadBatteryPrompt = prefs.contains("battery_opt_asked")
            oldBatteryPrompt = prefs.getBoolean("battery_opt_asked", false)
            assertTrue(prefs.edit().putBoolean("battery_opt_asked", true).commit())
            val info = instrumentation.uiAutomation.serviceInfo
            oldAccessibilityFlags = info.flags
            info.flags = info.flags or AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS
            instrumentation.uiAutomation.serviceInfo = info
        }

        override fun after() {
            oldTilePrompt?.let { Settings.qsTilePrompted = it }
                ?: Settings.dataStore.remove(SettingsKey.QS_TILE_PROMPTED)
            val edit = context.getSharedPreferences("maestro_prefs", Context.MODE_PRIVATE).edit()
            if (hadBatteryPrompt) edit.putBoolean("battery_opt_asked", oldBatteryPrompt)
            else edit.remove("battery_opt_asked")
            assertTrue(edit.commit())
            val info = instrumentation.uiAutomation.serviceInfo
            info.flags = oldAccessibilityFlags
            instrumentation.uiAutomation.serviceInfo = info
            if (Build.VERSION.SDK_INT >= 33) {
                oldApplicationLocales?.let { locales ->
                    instrumentation.runOnMainSync {
                        context.getSystemService(LocaleManager::class.java).applicationLocales = locales
                    }
                }
            }
        }
    }
    private val failureCapture = object : TestWatcher() {
        override fun failed(error: Throwable, description: Description) {
            // This watcher runs while the activity rule still owns the activity; no Compose
            // synchronization is required, so even an unexpected system dialog is captured.
            runCatching { rawShot("failure-${description.methodName}") }.exceptionOrNull()
                ?.let { error.addSuppressed(it) }
        }
    }
    @get:Rule val rules: RuleChain = RuleChain.outerRule(startupPrompts).around(ui).around(failureCapture)

    @Before fun preserveSettings() {
        oldSelection = Settings.selectedProfile
        oldApps = Settings.perAppProxyList.toSet()
        oldMode = Settings.perAppProxyMode
        oldEnabled = Settings.perAppProxyEnabled
        assertEquals("Phone captures must exercise Russian resources", "ru", ui.activity.resources.configuration.locales[0].language)
        waitForAppWindow()
        val roots = ui.onAllNodes(isRoot())
        ui.waitUntil(15_000) { roots.fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
    }

    @After fun removeOnlyFixture() {
        ui.activityRule.scenario.onActivity {
            Settings.selectedProfile = oldSelection
            Settings.perAppProxyList = oldApps
            Settings.perAppProxyMode = oldMode
            Settings.perAppProxyEnabled = oldEnabled
        }
        fixture?.let { runBlocking(Dispatchers.IO) { ProfileManager.delete(it) } }
        fixtureFile?.delete()
    }

    @Test fun pagesAndKeyboardKeepActionsReachable() {
        node("Ввести логин").assertIsDisplayed()
        shot("main-empty")
        tap("Ввести логин")
        ui.onNodeWithContentDescription("Введите логин").performClick().performTextInput("fixture-login")
        waitForIme(visible = true)
        node("Войти").assertIsDisplayed().assertIsEnabled()
        shot("login-keyboard")
        pressBack() // IME, then the login page; no claim is submitted.
        waitForIme(visible = false)
        tap("Главная")
        tap("Серверы"); node("Обновить список").assertIsDisplayed(); shot("servers")
        tap("Подписка"); node("Моя подписка").assertIsDisplayed(); shot("subscription-empty")
        tap("Главная"); tap("CDN"); node("Обновить").assertIsDisplayed(); shot("cdn-empty")
        tap("Настройки"); node("Обновление приложения").assertIsDisplayed(); shot("settings")
        tap("Сканировать QR-код"); shot("qr-scanner"); pressBack()
        tap("Обновление приложения"); node("Проверить обновления").assertIsDisplayed(); shot("update")
        tap("Настройки"); tap("Дополнительные настройки")
        waitForHeader(com.maestrovpn.tv.R.string.title_settings)
        node(ui.activity.getString(com.maestrovpn.tv.R.string.title_app_settings)).assertIsDisplayed()
        shot("advanced-settings")
        // Read-only child pages, retaining their real back actions and geometry.
        for (title in listOf(com.maestrovpn.tv.R.string.title_app_settings,
            com.maestrovpn.tv.R.string.core, com.maestrovpn.tv.R.string.service,
            com.maestrovpn.tv.R.string.profile_override, com.maestrovpn.tv.R.string.remote_control)) {
            tap(ui.activity.getString(title), scroll = true)
            waitForHeader(title)
            shot("settings-$title")
            pressBack()
            waitForHeader(com.maestrovpn.tv.R.string.title_settings)
        }
        tap("Главная")
    }

    @Test fun applicationChoiceIsSavedOnlyBySaveButton() {
        shot("before-app-selection-navigation")
        tap("Настройки"); tap("Приложения и VPN")
        val toggles = ui.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsProperties.ToggleableState))
        ui.waitUntil(20_000) { toggles.fetchSemanticsNodes().isNotEmpty() }
        toggles[0].performClick()
        assertEquals("Selection must remain a draft", oldApps, Settings.perAppProxyList)
        shot("apps-draft")
        node("Сохранить").assertIsDisplayed().performClick()
        ui.waitForIdle()
        assertTrue(Settings.perAppProxyEnabled)
        assertTrue(Settings.perAppProxyList.isNotEmpty())
        val saved = Settings.perAppProxyList.toSet()
        tap("Главная"); tap("Настройки"); tap("Приложения и VPN")
        node("Сохранить").assertIsDisplayed()
        assertEquals(saved, Settings.perAppProxyList)
        shot("apps-saved")
    }

    @Test fun sharedLinksUseChosenClientAndCloseRemainsVisible() {
        val key = "ui-${UUID.randomUUID()}"
        val file = File(context.cacheDir, "$key.json").also {
            it.writeText("""{"outbounds":[{"type":"direct","tag":"direct"}]}""")
        }
        fixtureFile = file
        // .invalid deliberately prevents updater/balance traffic to any real account.
        fixture = runBlocking(Dispatchers.IO) { ProfileManager.create(Profile(name = key,
            userOrder = ProfileManager.nextOrder(), typed = TypedProfile().apply {
                type = TypedProfile.Type.Remote
                remoteURL = "https://example.invalid/sub/$key?device=sender-device&platform=mobile"
                path = file.path
                autoUpdate = false
            })) }
        ui.runOnIdle { Settings.selectedProfile = fixture!!.id }
        tap("Настройки"); tap("Подключить устройство")
        for ((client, format) in listOf("Happ" to "xray", "Incy" to "xray", "Karing" to "links", "MaestroVPN" to null)) {
            tap(client, scroll = true)
            node("Закрыть").assertIsDisplayed()
            shot("before-copy-$client")
            tap("Скопировать ссылку", scroll = true)
            waitForAppWindow()
            val uri = ui.runOnIdle {
                val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                val clip = clipboard.primaryClip
                assertNotNull("Copy must populate the clipboard while the app owns window focus", clip)
                Uri.parse(clip!!.getItemAt(0).text.toString())
            }
            assertEquals(format, uri.getQueryParameter("format"))
            assertNull(uri.getQueryParameter("device"))
            assertNull(uri.getQueryParameter("platform"))
        }
        shot("share-fixture")
        tap("Закрыть")
        node("Настройки").assertIsDisplayed()
    }

    private fun pressBack() {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        ui.waitForIdle()
    }

    private fun node(text: String): SemanticsNodeInteraction {
        val matches = ui.onAllNodesWithText(text)
        ui.waitUntil(15_000) { matches.fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
        return matches[matches.fetchSemanticsNodes(atLeastOneRootRequired = false).lastIndex]
    }
    private fun waitForHeader(titleId: Int) {
        val title = ui.activity.getString(titleId)
        // A settings row can contain the same text as the destination title. Its click
        // action distinguishes that old row from the destination's non-clickable app bar.
        val headers = ui.onAllNodes(hasText(title) and !hasClickAction())
        ui.waitUntil(15_000) { headers.fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
        headers[headers.fetchSemanticsNodes(atLeastOneRootRequired = false).lastIndex].assertIsDisplayed()
    }
    private fun tap(text: String, scroll: Boolean = false) {
        waitForAppWindow()
        val item = node(text)
        if (scroll) item.performScrollTo()
        item.performClick()
        ui.waitForIdle()
    }
    private fun shot(name: String) {
        ui.waitForIdle()
        waitForAppWindow()
        val committed = CountDownLatch(1)
        var waitForCommit = false
        ui.runOnUiThread {
            val view = ui.activity.window.decorView
            // A scanner activity or an app dialog can own another window. In that case the
            // native focus check still applies, but an obscured MainActivity cannot commit.
            if (Build.VERSION.SDK_INT >= 29 && view.hasWindowFocus() && view.isHardwareAccelerated) {
                view.viewTreeObserver.registerFrameCommitCallback { committed.countDown() }
                waitForCommit = true
                view.invalidate()
            }
        }
        if (waitForCommit) assertTrue("The displayed app frame must be committed", committed.await(5, TimeUnit.SECONDS))
        SystemClock.sleep(250) // Let the committed frame and IME/dialog surface reach the native screenshot.
        waitForAppWindow()
        rawShot(name)
    }

    private fun waitForAppWindow() {
        ui.waitUntil(15_000) {
            val windows = instrumentation.uiAutomation.windows
            try {
                windows.any { window ->
                    if (!window.isFocused) false
                    else {
                        val root = window.root
                        try { root?.packageName?.toString() == context.packageName }
                        finally { root?.recycle() }
                    }
                }
            } finally { windows.forEach { it.recycle() } }
        }
    }

    private fun waitForIme(visible: Boolean) {
        ui.waitUntil(10_000) {
            ui.runOnUiThread {
                val insets = ViewCompat.getRootWindowInsets(ui.activity.window.decorView)
                insets != null && insets.isVisible(WindowInsetsCompat.Type.ime()) == visible &&
                    (!visible || insets.getInsets(WindowInsetsCompat.Type.ime()).bottom > 0)
            }
        }
    }

    private fun rawShot(name: String) {
        val bitmap = instrumentation.uiAutomation.takeScreenshot()
        assertNotNull("Native screenshot must be available", bitmap)
        val dir = context.getExternalFilesDir("ui-captures")!!.also { it.mkdirs() }
        File(dir, "$name.png").outputStream().use { bitmap!!.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap!!.recycle()
    }
}

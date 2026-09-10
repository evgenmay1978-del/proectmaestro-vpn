package com.maestrovpn.tv.compose

import android.content.ClipboardManager
import android.content.Context
import android.graphics.Bitmap
import android.net.Uri
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.espresso.Espresso.pressBack
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.ext.junit.runners.AndroidJUnit4
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
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID

/** Real navigation on the disposable emulator; no account registration or payment submission. */
@RunWith(AndroidJUnit4::class)
class PhoneNavigationInstrumentedTest {
    @get:Rule val ui = createAndroidComposeRule<MainActivity>()
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private var oldSelection = -1L
    private var oldApps = emptySet<String>()
    private var oldMode = 0
    private var oldEnabled = false
    private var fixture: Profile? = null
    private var fixtureFile: File? = null

    @Before fun preserveSettings() {
        oldSelection = Settings.selectedProfile
        oldApps = Settings.perAppProxyList.toSet()
        oldMode = Settings.perAppProxyMode
        oldEnabled = Settings.perAppProxyEnabled
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
        node("Войти").assertIsDisplayed().assertIsEnabled()
        shot("login-keyboard")
        pressBack() // IME, then the login page; no claim is submitted.
        tap("Главная")
        tap("Серверы"); node("Обновить список").assertIsDisplayed(); shot("servers")
        tap("Подписка"); node("Моя подписка").assertIsDisplayed(); shot("subscription-empty")
        tap("Главная"); tap("CDN"); node("Обновить").assertIsDisplayed(); shot("cdn-empty")
        tap("Настройки"); node("Обновление приложения").assertIsDisplayed(); shot("settings")
        tap("Сканировать QR-код"); shot("qr-scanner"); pressBack()
        tap("Обновление приложения"); node("Проверить обновления").assertIsDisplayed(); shot("update")
        tap("Настройки"); tap("Дополнительные настройки"); shot("advanced-settings")
        // Read-only child pages, retaining their real back actions and geometry.
        for (title in listOf(com.maestrovpn.tv.R.string.title_app_settings,
            com.maestrovpn.tv.R.string.core, com.maestrovpn.tv.R.string.service,
            com.maestrovpn.tv.R.string.profile_override, com.maestrovpn.tv.R.string.remote_control)) {
            tap(context.getString(title), scroll = true)
            shot("settings-$title")
            pressBack()
        }
        tap("Главная")
    }

    @Test fun applicationChoiceIsSavedOnlyBySaveButton() {
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
            tap("Скопировать ссылку", scroll = true)
            val uri = ui.runOnIdle {
                val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                Uri.parse(clipboard.primaryClip!!.getItemAt(0).text.toString())
            }
            assertEquals(format, uri.getQueryParameter("format"))
            assertNull(uri.getQueryParameter("device"))
            assertNull(uri.getQueryParameter("platform"))
        }
        shot("share-fixture")
        tap("Закрыть")
        node("Настройки").assertIsDisplayed()
    }

    private fun node(text: String): SemanticsNodeInteraction {
        val matches = ui.onAllNodesWithText(text)
        ui.waitUntil(15_000) { matches.fetchSemanticsNodes().isNotEmpty() }
        return matches[matches.fetchSemanticsNodes().lastIndex]
    }
    private fun tap(text: String, scroll: Boolean = false) {
        val item = node(text)
        if (scroll) item.performScrollTo()
        item.performClick()
        ui.waitForIdle()
    }
    private fun shot(name: String) {
        ui.waitForIdle()
        val bitmap = InstrumentationRegistry.getInstrumentation().uiAutomation.takeScreenshot()
        assertNotNull("Native screenshot must be available", bitmap)
        val dir = context.getExternalFilesDir("ui-captures")!!.also { it.mkdirs() }
        File(dir, "$name.png").outputStream().use { bitmap!!.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap!!.recycle()
    }
}

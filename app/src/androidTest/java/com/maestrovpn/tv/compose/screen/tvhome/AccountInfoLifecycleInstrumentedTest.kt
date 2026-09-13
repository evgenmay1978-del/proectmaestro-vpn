package com.maestrovpn.tv.compose.screen.tvhome

import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.lifecycle.ProcessLifecycleOwner
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.TypedProfile
import com.maestrovpn.tv.utils.AppLifecycleObserver
import com.maestrovpn.tv.utils.DeviceFormFactor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger

/** Real phone composition and profile store, with synthetic read-only account responses. */
@RunWith(AndroidJUnit4::class)
class AccountInfoLifecycleInstrumentedTest {
    @get:Rule val ui = createAndroidComposeRule<ComponentActivity>()
    private var previousSelection = -1L
    private var previousForeground = true
    private var fixture: Profile? = null
    private var fixtureFile: File? = null
    private val mounted = mutableStateOf(true)
    private var contentMounted = false

    @Before fun createOnlyDisposableAccount() {
        assertFalse("Lifecycle fixture requires a phone", DeviceFormFactor.isTelevision(ui.activity))
        previousSelection = Settings.selectedProfile
        previousForeground = AppLifecycleObserver.isForeground.value
        runBlocking(Dispatchers.IO) {
            assertTrue("Lifecycle fixture requires an empty disposable profile store", ProfileManager.list().isEmpty())
            val token = UUID.randomUUID().toString().replace("-", "")
            val file = File(ui.activity.filesDir, "account-lifecycle-$token.json")
            file.writeText("{}")
            fixtureFile = file
            val profile = Profile(name = "account-lifecycle-fixture", typed = TypedProfile().apply {
                type = TypedProfile.Type.Remote
                remoteURL = "https://wapmixx.ru/sub/$token?platform=mobile"
                path = file.path
            })
            ProfileManager.create(profile, andSelect = true)
            fixture = ProfileManager.list().single()
        }
        ui.runOnUiThread { AppLifecycleObserver.onStart(ProcessLifecycleOwner.get()) }
    }

    @After fun removeOnlyOwnedAccount() {
        if (contentMounted) ui.runOnIdle { mounted.value = false }
        Settings.selectedProfile = previousSelection
        fixture?.let { runBlocking(Dispatchers.IO) { ProfileManager.delete(it) } }
        fixtureFile?.let { assertTrue("Could not remove lifecycle fixture", it.delete() || !it.exists()) }
        ui.runOnUiThread {
            if (previousForeground) AppLifecycleObserver.onStart(ProcessLifecycleOwner.get())
            else AppLifecycleObserver.onStop(ProcessLifecycleOwner.get())
        }
    }

    @Test fun returningFromBotRefreshesPaidTermWithoutVpnReconnect() {
        val refresh = mutableStateOf(0)
        val days = AtomicInteger(30)
        val reads = AtomicInteger(0)
        ui.setContent {
            if (mounted.value) {
                val info by rememberAccountInfo(refresh.value) {
                    reads.incrementAndGet()
                    JSONObject().put("login", "lifecycle-fixture").put("days_left", days.get())
                        .put("expires", "2030-11-12T14:55:08Z").toString()
                }
                Text("${info.login.orEmpty()} · ${info.daysLeft ?: 0}")
            }
        }
        contentMounted = true
        ui.waitUntil(10_000) { reads.get() == 1 &&
            ui.onAllNodesWithText("lifecycle-fixture · 30").fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
        ui.onNodeWithText("lifecycle-fixture · 30").assertIsDisplayed()

        ui.runOnIdle { AppLifecycleObserver.onStop(ProcessLifecycleOwner.get()) }
        ui.waitForIdle()
        days.set(60)
        ui.runOnIdle { refresh.value++ }
        ui.waitForIdle()
        assertEquals("Background changes must not fetch or clear the displayed account", 1, reads.get())
        ui.onNodeWithText("lifecycle-fixture · 30").assertIsDisplayed()

        ui.runOnIdle { AppLifecycleObserver.onStart(ProcessLifecycleOwner.get()) }
        ui.waitUntil(10_000) { reads.get() == 2 &&
            ui.onAllNodesWithText("lifecycle-fixture · 60").fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
        ui.onNodeWithText("lifecycle-fixture · 60").assertIsDisplayed()
        ui.runOnIdle { AppLifecycleObserver.onStart(ProcessLifecycleOwner.get()) }
        ui.waitForIdle()
        assertEquals("An unchanged foreground state must not start another refresh", 2, reads.get())
        assertEquals(fixture!!.id, Settings.selectedProfile)
    }
}

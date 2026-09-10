package com.maestrovpn.tv.compose.screen.purchase

import android.app.Application
import android.content.Context
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.viewModelScope
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.TypedProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

/** Runs only on a disposable emulator: real Android persistence, entirely synthetic HTTP. */
@RunWith(AndroidJUnit4::class)
class BuyViewModelInstrumentedTest {
    private lateinit var application: Application
    private var previousSelection: Long? = null
    private val fixturePrefix = "buy-fixture-${UUID.randomUUID()}"
    private val profiles = mutableListOf<Profile>()
    private val files = mutableListOf<File>()
    private val receiptKeys = mutableSetOf<String>()
    private val stores = linkedMapOf<BuyViewModel, ViewModelStore>()
    private var fixtureDeviceId: String? = null
    private val base = BuildConfig.BACKEND_URL.trimEnd('/')
    private val receipts get() = application.getSharedPreferences("pending-vpn-order", Context.MODE_PRIVATE)

    @Before
    fun requireDisposableProfileDatabase() {
        application = InstrumentationRegistry.getInstrumentation().targetContext.applicationContext as Application
        previousSelection = Settings.selectedProfile
        runBlocking(Dispatchers.IO) {
            assertTrue("Purchase fixtures require a disposable emulator with no profiles", ProfileManager.list().isEmpty())
        }
    }

    @After
    fun removeOnlyOwnedFixtures() {
        try {
            stores.keys.toList().forEach(::close)
        } finally {
            previousSelection?.let { Settings.selectedProfile = it }
            if (::application.isInitialized) {
                val edit = receipts.edit()
                receiptKeys.forEach { edit.remove(it) }
                assertTrue(edit.commit())
                runBlocking(Dispatchers.IO) { profiles.asReversed().forEach { ProfileManager.delete(it) } }
                files.forEach { assertTrue("Could not remove fixture ${it.name}", it.delete() || !it.exists()) }
                fixtureDeviceId?.let { ownedId ->
                    val devicePrefs = application.getSharedPreferences("maestro_device", Context.MODE_PRIVATE)
                    if (devicePrefs.getString("device_id", null) == ownedId) {
                        assertTrue(devicePrefs.edit().remove("device_id").commit())
                    }
                }
            }
        }
    }

    @Test
    fun renewalUsesSelectedProfileInsteadOfFirstProfile() {
        // Reflection verifies the constructor required by AndroidViewModelFactory without invoking real HTTP.
        assertNotNull(BuyViewModel::class.java.getConstructor(Application::class.java))
        val first = profile("first")
        val selected = profile("selected")
        Settings.selectedProfile = selected.id
        assertEquals(first.id, runBlocking(Dispatchers.IO) { ProfileManager.list().first().id })
        val backend = FixtureOrders(base)
        val model = model(backend)
        awaitState<BuyState.Tariffs>(model)

        onMain { model.buy("month") }
        awaitState<BuyState.AwaitingPayment>(model)

        val order = backend.orders.single()
        assertEquals("month", order.getString("tariff"))
        assertEquals("$fixturePrefix-selected", order.getString("sub_token"))
        assertEquals(selected.id, Settings.selectedProfile)
        assertTrue(receipts.contains(receiptKey(selected)))
        assertFalse(receipts.contains(receiptKey(first)))
    }

    @Test
    fun pendingReceiptSurvivesViewModelRecreationWithoutAnotherOrder() {
        val selected = profile("selected")
        Settings.selectedProfile = selected.id
        val backend = FixtureOrders(base)
        val original = model(backend)
        awaitState<BuyState.Tariffs>(original)
        onMain { original.buy("month") }
        val invoice = awaitState<BuyState.AwaitingPayment>(original)
        close(original)

        val reopened = model(backend)
        assertEquals(invoice, awaitState<BuyState.AwaitingPayment>(reopened))
        onMain { reopened.buy("month") }

        assertEquals(invoice, reopened.state.value)
        assertEquals(1, backend.orders.size)
        assertEquals(1, backend.tariffReads.get())
        assertEquals(backend.orderId, JSONObject(receipts.getString(receiptKey(selected), null)!!).getString("order_id"))
    }

    @Test
    fun restoredPaidClaimChecksStatusWithoutSendingTheClaimAgain() {
        val selected = profile("selected")
        Settings.selectedProfile = selected.id
        val backend = FixtureOrders(base)
        val original = model(backend)
        awaitState<BuyState.Tariffs>(original)
        onMain { original.buy("month") }
        awaitState<BuyState.AwaitingPayment>(original)
        onMain { original.iPaid() }
        // A synthetic status read failure leaves the claim and receipt available for retry.
        awaitState<BuyState.Error>(original)
        assertEquals(listOf(backend.orderId), backend.claims.toList())
        assertTrue(JSONObject(receipts.getString(receiptKey(selected), null)!!).getBoolean("claim_sent"))
        close(original)

        val reopened = model(backend)
        awaitState<BuyState.Error>(reopened)
        assertEquals(2, backend.statusReads.get())
        onMain { reopened.retry() }
        awaitState<BuyState.Error>(reopened)

        assertEquals(3, backend.statusReads.get())
        assertEquals(listOf(backend.orderId), backend.claims.toList())
        assertEquals(1, backend.orders.size)
        assertTrue(receipts.contains(receiptKey(selected)))
    }

    @Test
    fun confirmedRenewalValidatesConfigAndUpdatesExistingProfileWithoutDuplicates() {
        val first = profile("first")
        val selected = profile("selected")
        Settings.selectedProfile = selected.id
        val originalFirstConfig = File(first.typed.path).readText()
        val config = """{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}"""
        val fetchedUrls = CopyOnWriteArrayList<String>()
        val backend = FixtureOrders(base) {
            JSONObject().put("status", "paid")
                .put("sub_url", "$base/sub/$fixturePrefix-selected").toString()
        }
        val devicePrefs = application.getSharedPreferences("maestro_device", Context.MODE_PRIVATE)
        if (!devicePrefs.contains("device_id")) {
            fixtureDeviceId = fixturePrefix
            assertTrue(devicePrefs.edit().putString("device_id", fixtureDeviceId).commit())
        }
        val model = model(backend) { url -> fetchedUrls.add(url); config }
        awaitState<BuyState.Tariffs>(model)
        onMain { model.buy("month") }
        awaitState<BuyState.AwaitingPayment>(model)
        onMain { model.iPaid() }
        // Done is reachable only after the production Libbox.checkConfig and Room update succeed.
        awaitState<BuyState.Done>(model)

        val remaining = runBlocking(Dispatchers.IO) { ProfileManager.list() }
        assertEquals(setOf(first.id, selected.id), remaining.map { it.id }.toSet())
        assertEquals(2, remaining.size)
        val updated = remaining.single { it.id == selected.id }
        assertEquals(config, File(updated.typed.path).readText())
        assertEquals(originalFirstConfig, File(first.typed.path).readText())
        assertTrue(updated.typed.lastUpdated.time > 0L)
        assertFalse(updated.typed.autoUpdate)
        assertEquals(selected.id, Settings.selectedProfile)
        assertEquals("$base/sub/$fixturePrefix-selected", fetchedUrls.single().substringBefore('?'))
        assertEquals(fetchedUrls.single(), updated.typed.remoteURL)
        assertFalse(receipts.contains(receiptKey(selected)))
        assertEquals(1, backend.orders.size)
        assertEquals(listOf(backend.orderId), backend.claims.toList())
    }

    private fun profile(label: String): Profile = runBlocking(Dispatchers.IO) {
        val file = File(application.cacheDir, "$fixturePrefix-$label.json")
        files.add(file)
        file.writeText("""{"outbounds":[{"type":"direct","tag":"before-$label"}]}""")
        val created = ProfileManager.create(Profile(
            name = "$fixturePrefix-$label",
            userOrder = ProfileManager.nextOrder(),
            typed = TypedProfile().apply {
                type = TypedProfile.Type.Remote
                remoteURL = "$base/sub/$fixturePrefix-$label?device=fixture-old&platform=mobile"
                path = file.path
                autoUpdate = false
            },
        ))
        profiles.add(created)
        val key = receiptKey(created)
        assertFalse("Fixture must not overwrite an existing receipt", receipts.contains(key))
        receiptKeys.add(key)
        created
    }

    private fun receiptKey(profile: Profile) = "profile-${profile.id}"

    private fun model(
        backend: FixtureOrders,
        fetchSubscription: suspend (String) -> String? = { error("Unexpected subscription request: $it") },
    ): BuyViewModel {
        lateinit var created: BuyViewModel
        onMain {
            created = BuyViewModel(application, backend::get, backend::post, fetchSubscription)
            stores[created] = ViewModelStore().also { it.put("purchase-fixture", created) }
        }
        return created
    }

    private fun close(model: BuyViewModel) {
        val store = stores.remove(model) ?: return
        val job = model.viewModelScope.coroutineContext[Job]
        onMain { store.clear() }
        runBlocking { withTimeout(10_000) { job?.join() } }
    }

    private inline fun <reified T : BuyState> awaitState(model: BuyViewModel): T = runBlocking {
        withTimeout(10_000) {
            val actual = model.state.first { it is T || it is BuyState.Error }
            assertTrue("Expected ${T::class.java.simpleName}, got $actual", actual is T)
            actual as T
        }
    }

    private fun onMain(block: () -> Unit) {
        InstrumentationRegistry.getInstrumentation().runOnMainSync { block() }
    }

    private class FixtureOrders(
        private val base: String,
        private val status: () -> String = { throw IOException("Synthetic status read failure") },
    ) {
        val orderId = "fixture-order-${UUID.randomUUID()}"
        val orders = CopyOnWriteArrayList<JSONObject>()
        val claims = CopyOnWriteArrayList<String>()
        val tariffReads = AtomicInteger()
        val statusReads = AtomicInteger()

        fun get(url: String): String = when (url) {
            "$base/order/tariffs" -> {
                tariffReads.incrementAndGet()
                """{"tariffs":[{"key":"month","name":"Fixture month","rub":199}],"sbp_phone":"+7 000 000-00-00"}"""
            }
            "$base/order/$orderId" -> { statusReads.incrementAndGet(); status() }
            else -> error("Unexpected fixture GET: $url")
        }

        fun post(url: String, body: String): String = when (url) {
            "$base/order" -> {
                orders.add(JSONObject(body))
                JSONObject().put("order_id", orderId).put("rub", 199).put("code", "FIXTURE")
                    .put("sbp_phone", "+7 000 000-00-00")
                    .put("pay_url", "https://payments.invalid/$orderId").toString()
            }
            "$base/order/paid-claim" -> {
                claims.add(JSONObject(body).getString("order_id"))
                "{}"
            }
            else -> error("Unexpected fixture POST: $url")
        }
    }
}

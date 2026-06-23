package com.maestrovpn.tv.compose.screen.purchase

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.TypedProfile
import com.maestrovpn.tv.utils.HTTPClient
import com.maestrovpn.tv.utils.MaestroSub
import io.nekohasekai.libbox.Libbox
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.util.Date

/** One purchasable plan for the UI. */
data class TariffItem(val key: String, val name: String, val rub: Int)

/** The Buy screen state machine. */
sealed interface BuyState {
    data object Loading : BuyState
    data class Tariffs(val items: List<TariffItem>, val phone: String) : BuyState
    data class AwaitingPayment(val rub: Int, val code: String, val phone: String, val payUrl: String) : BuyState
    data object AwaitingConfirm : BuyState
    data object Activating : BuyState
    data object Done : BuyState
    data class Error(val message: String) : BuyState
}

/**
 * Drives the in-app СБП purchase: load tariffs → create an order → show the СБП
 * details → poll the order until the owner confirms payment → register the
 * subscription as an auto-updating Remote profile. All D-pad, no text entry.
 */
class BuyViewModel(application: Application) : AndroidViewModel(application) {
    private val _state = MutableStateFlow<BuyState>(BuyState.Loading)
    val state = _state.asStateFlow()
    private val base = BuildConfig.BACKEND_URL.trimEnd('/')

    init {
        loadTariffs()
    }

    fun loadTariffs() {
        _state.value = BuyState.Loading
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val o = JSONObject(httpGet("$base/order/tariffs"))
                val arr = o.getJSONArray("tariffs")
                val items = (0 until arr.length()).map {
                    val t = arr.getJSONObject(it)
                    TariffItem(t.getString("key"), t.getString("name"), t.getInt("rub"))
                }
                _state.value = BuyState.Tariffs(items, o.optString("sbp_phone"))
            } catch (e: Exception) {
                _state.value = BuyState.Error(e.message ?: "не удалось загрузить тарифы")
            }
        }
    }

    private var orderId: String? = null

    fun buy(tariffKey: String) {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val body = JSONObject().put("tariff", tariffKey)
                // If the user already has a MaestroVPN subscription, RENEW that same
                // account: send its sub_token (from the profile's /sub/<token> URL) so the
                // backend extends the SAME login/keys across all 4 panels instead of minting
                // a brand-new account each time. No subscription yet → omitted → new account.
                runCatching {
                    ProfileManager.list().firstNotNullOfOrNull {
                        MaestroSub.token(it.typed.remoteURL).takeIf { t -> t.isNotBlank() }
                    }
                }.getOrNull()?.let { body.put("sub_token", it) }
                val o = JSONObject(httpPost("$base/order", body.toString()))
                orderId = o.getString("order_id")
                _state.value = BuyState.AwaitingPayment(o.getInt("rub"), o.getString("code"), o.optString("sbp_phone"), o.optString("pay_url"))
            } catch (e: Exception) {
                _state.value = BuyState.Error(e.message ?: "ошибка")
            }
        }
    }

    /** Customer pressed «Я оплатил»: notify the owner, then poll until confirmed. */
    fun iPaid() {
        val id = orderId ?: return
        _state.value = BuyState.AwaitingConfirm
        viewModelScope.launch(Dispatchers.IO) {
            try {
                httpPost("$base/order/paid-claim", JSONObject().put("order_id", id).toString())
                while (true) {
                    delay(4000)
                    val po = JSONObject(httpGet("$base/order/$id"))
                    if (po.optString("status") == "paid") {
                        _state.value = BuyState.Activating
                        activate(po.getString("sub_url"))
                        _state.value = BuyState.Done
                        return@launch
                    }
                }
            } catch (e: Exception) {
                _state.value = BuyState.Error(e.message ?: "ошибка")
            }
        }
    }

    private suspend fun activate(subUrl: String) {
        val context = getApplication<Application>()
        // Carry this install's device id so the renewed profile keeps counting against the cap.
        val devUrl = MaestroSub.withDevice(context, subUrl)
        val content = HTTPClient().use { it.getString(devUrl) }
        Libbox.checkConfig(content)
        // A renewal returns the SAME account — match by sub TOKEN (the device query differs)
        // and refresh the existing profile (also upgrading its URL to carry the device id)
        // instead of piling up duplicate "MaestroVPN" entries. Only a first activation
        // creates a new profile.
        val existing = ProfileManager.list().firstOrNull { MaestroSub.token(it.typed.remoteURL) == MaestroSub.token(devUrl) }
        if (existing != null) {
            existing.typed.remoteURL = devUrl
            File(existing.typed.path).writeText(content)
            existing.typed.lastUpdated = Date()
            ProfileManager.update(existing)
            Settings.selectedProfile = existing.id
            UpdateProfileWork.reconfigureUpdater()
            return
        }
        val typed = TypedProfile().apply {
            type = TypedProfile.Type.Remote
            remoteURL = devUrl
            autoUpdate = true
            autoUpdateInterval = 15
            lastUpdated = Date()
        }
        val profile = Profile(name = "MaestroVPN", typed = typed).apply {
            userOrder = ProfileManager.nextOrder()
        }
        val fileID = ProfileManager.nextFileID()
        val dir = File(context.filesDir, "configs").also { it.mkdirs() }
        val file = File(dir, "$fileID.json")
        typed.path = file.path
        file.writeText(content)
        ProfileManager.create(profile, andSelect = true)
        UpdateProfileWork.reconfigureUpdater()
    }

    private fun httpGet(url: String): String {
        val c = URL(url).openConnection() as HttpURLConnection
        try {
            c.connectTimeout = 15000
            c.readTimeout = 15000
            if (c.responseCode != 200) throw IOException("HTTP ${c.responseCode}")
            return c.inputStream.bufferedReader().use { it.readText() }
        } finally {
            c.disconnect()
        }
    }

    private fun httpPost(url: String, json: String): String {
        val c = URL(url).openConnection() as HttpURLConnection
        try {
            c.requestMethod = "POST"
            c.doOutput = true
            c.connectTimeout = 15000
            c.readTimeout = 15000
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(json.toByteArray()) }
            if (c.responseCode != 200) throw IOException("HTTP ${c.responseCode}")
            return c.inputStream.bufferedReader().use { it.readText() }
        } finally {
            c.disconnect()
        }
    }
}

package com.maestrovpn.tv.compose.screen.purchase

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.TypedProfile
import com.maestrovpn.tv.utils.HTTPClient
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
    data class AwaitingPayment(val rub: Int, val code: String, val phone: String) : BuyState
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
                val o = JSONObject(httpPost("$base/order", JSONObject().put("tariff", tariffKey).toString()))
                orderId = o.getString("order_id")
                _state.value = BuyState.AwaitingPayment(o.getInt("rub"), o.getString("code"), o.optString("sbp_phone"))
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
        val typed = TypedProfile().apply {
            type = TypedProfile.Type.Remote
            remoteURL = subUrl
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
        val content = HTTPClient().use { it.getString(subUrl) }
        Libbox.checkConfig(content)
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

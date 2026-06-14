package com.maestrovpn.tv.compose.screen.claim

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
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.util.Date

/** Result of the install-time claim-code exchange. */
sealed interface ClaimState {
    data object Idle : ClaimState
    data object Busy : ClaimState
    data object Done : ClaimState
    data class Error(val message: String) : ClaimState
}

/**
 * Exchanges the short claim code for the customer's subscription URL (POST
 * BACKEND_URL/claim) and registers it as an auto-updating Remote profile — so the
 * key rotates invisibly (auto-key). Mirrors NewProfileViewModel.createRemoteProfile.
 */
class ClaimViewModel(application: Application) : AndroidViewModel(application) {
    private val _state = MutableStateFlow<ClaimState>(ClaimState.Idle)
    val state = _state.asStateFlow()

    fun claim(code: String) {
        if (_state.value is ClaimState.Busy) return
        _state.value = ClaimState.Busy
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val subUrl = fetchSubUrl(code.trim())
                createRemoteProfile(subUrl)
                _state.value = ClaimState.Done
            } catch (e: Exception) {
                _state.value = ClaimState.Error(e.message ?: "ошибка")
            }
        }
    }

    private fun fetchSubUrl(code: String): String {
        val conn = URL(BuildConfig.BACKEND_URL.trimEnd('/') + "/claim").openConnection() as HttpURLConnection
        try {
            conn.requestMethod = "POST"
            conn.doOutput = true
            conn.connectTimeout = 15000
            conn.readTimeout = 15000
            conn.setRequestProperty("Content-Type", "application/json")
            conn.outputStream.use { it.write(JSONObject().put("code", code).toString().toByteArray()) }
            return when (conn.responseCode) {
                200 -> JSONObject(conn.inputStream.bufferedReader().use { it.readText() }).getString("sub_url")
                404 -> throw IOException("код не найден")
                else -> throw IOException("сервер: HTTP ${conn.responseCode}")
            }
        } finally {
            conn.disconnect()
        }
    }

    private suspend fun createRemoteProfile(subUrl: String) {
        val context = getApplication<Application>()
        val typed = TypedProfile().apply {
            type = TypedProfile.Type.Remote
            remoteURL = subUrl
            autoUpdate = true
            autoUpdateInterval = 15 // WorkManager floor
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
}

package com.maestrovpn.tv.compose.screen.trial

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.TypedProfile
import com.maestrovpn.tv.utils.httpGetStringTimed
import com.maestrovpn.tv.utils.MaestroSub
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

/** Result of the in-app free-trial activation. */
sealed interface TrialState {
    data object Idle : TrialState
    data object Busy : TrialState
    data object Done : TrialState
    data class Error(val message: String) : TrialState
}

/**
 * Free-trial flow for users with NO key: the user enters any nickname, the backend
 * (POST BACKEND_URL/trial) provisions a short trial — gated by an anti-abuse ledger keyed
 * on the device's reinstall-surviving anchor ([MaestroSub.antiAbuseAnchor]) — and returns a
 * sub URL we register as an auto-updating Remote profile (same as [com.maestrovpn.tv.compose.screen.claim.ClaimViewModel]).
 */
class TrialViewModel(application: Application) : AndroidViewModel(application) {
    private val _state = MutableStateFlow<TrialState>(TrialState.Idle)
    val state = _state.asStateFlow()

    fun activate(nick: String) {
        if (_state.value is TrialState.Busy) return
        _state.value = TrialState.Busy
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val subUrl = MaestroSub.withDevice(getApplication<Application>(), fetchTrialSub(nick.trim()))
                createRemoteProfile(subUrl)
                _state.value = TrialState.Done
            } catch (e: Exception) {
                _state.value = TrialState.Error(e.message ?: "ошибка")
            }
        }
    }

    private fun fetchTrialSub(nick: String): String {
        val ctx = getApplication<Application>()
        val conn = URL(BuildConfig.BACKEND_URL.trimEnd('/') + "/trial").openConnection() as HttpURLConnection
        try {
            conn.requestMethod = "POST"
            conn.doOutput = true
            conn.connectTimeout = 15000
            conn.readTimeout = 15000
            conn.setRequestProperty("Content-Type", "application/json")
            val body = JSONObject()
                .put("nick", nick)
                .put("anchor", MaestroSub.antiAbuseAnchor(ctx)) // reinstall-surviving device anchor
                .put("device", MaestroSub.deviceId(ctx))
            conn.outputStream.use { it.write(body.toString().toByteArray()) }
            return when (conn.responseCode) {
                200 -> JSONObject(conn.inputStream.bufferedReader().use { it.readText() }).getString("sub_url")
                400 -> throw IOException("введите корректный ник (буквы и цифры)")
                403 -> throw IOException("на этом устройстве пробный период уже был использован")
                409 -> throw IOException("этот ник уже занят — выберите другой")
                429 -> throw IOException("слишком много активаций с вашей сети — попробуйте позже")
                else -> throw IOException("сервер: HTTP ${conn.responseCode}")
            }
        } finally {
            conn.disconnect()
        }
    }

    // Identical to ClaimViewModel.createRemoteProfile — registers the sub as an auto-updating profile.
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
        val content = httpGetStringTimed(subUrl) ?: error("подписка недоступна (таймаут)")
        Libbox.checkConfig(content)
        file.writeText(content)
        ProfileManager.create(profile, andSelect = true)
        UpdateProfileWork.reconfigureUpdater()
    }
}

package com.maestrovpn.tv.compose.screen.claim

import android.app.Application
import android.util.Base64
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.database.Profile
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
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
import java.util.UUID

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
                // Bake this install's device id into the stored sub URL so EVERY auto-update
                // poll carries it and counts against the account's device cap.
                val subUrl = MaestroSub.withDevice(getApplication<Application>(), fetchSubUrl(code.trim()))
                createRemoteProfile(subUrl)
                _state.value = ClaimState.Done
            } catch (e: Exception) {
                _state.value = ClaimState.Error(e.message ?: "ошибка")
            }
        }
    }

    /**
     * Activate from an already-known subscription URL — e.g. one scanned from a QR code.
     * Same as [claim] but the URL IS the sub URL, so no /claim code exchange is needed.
     */
    fun importSubUrl(subUrl: String) {
        if (_state.value is ClaimState.Busy) return
        _state.value = ClaimState.Busy
        viewModelScope.launch(Dispatchers.IO) {
            try {
                // A shared /sub QR carries the SHARER's ?device=/?platform=; strip them so
                // withDevice re-stamps THIS device — otherwise the scanning device inherits the
                // sharer's id and the account's device cap is silently bypassed.
                val cleaned = MaestroSub.stripDeviceMetadata(subUrl.trim())
                createRemoteProfile(MaestroSub.withDevice(getApplication<Application>(), cleaned))
                _state.value = ClaimState.Done
            } catch (e: Exception) {
                _state.value = ClaimState.Error(e.message ?: "ошибка")
            }
        }
    }

    private fun fetchSubUrl(code: String): String {
        for (cdn in listOf(false, true)) {
            val endpoint = if (cdn) MaestroSub.CDN_ORIGIN + "/cabinet/api/claim"
                else BuildConfig.BACKEND_URL.trimEnd('/') + "/claim"
            var conn: HttpURLConnection? = null
            var status = 0
            try {
                val request = URL(endpoint).openConnection() as HttpURLConnection
                conn = request
                request.requestMethod = if (cdn) "GET" else "POST"
                request.instanceFollowRedirects = false
                request.useCaches = false
                request.connectTimeout = 15000
                request.readTimeout = 15000
                request.setRequestProperty("Accept", "application/json")
                request.setRequestProperty("Cache-Control", "no-store")
                if (cdn) {
                    val command = JSONObject().put("method", "POST")
                        .put("body", JSONObject().put("code", code)).toString().toByteArray(Charsets.UTF_8)
                    request.setRequestProperty("X-Maestro-Command", Base64.encodeToString(command,
                        Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP))
                    request.setRequestProperty("Idempotency-Key", UUID.randomUUID().toString())
                } else {
                    request.doOutput = true
                    request.setRequestProperty("Content-Type", "application/json")
                    val body = JSONObject().put("code", code).put("device", MaestroSub.deviceId(getApplication<Application>()))
                    request.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
                }
                status = request.responseCode
                return when (status) {
                    200 -> JSONObject(request.inputStream.bufferedReader().use { it.readText() }).getString("sub_url")
                    401 -> throw IOException("логин не принят")
                    403 -> throw IOException("достигнут лимит 5 устройств — отвяжите лишнее или напишите в поддержку")
                    404 -> throw IOException("код не найден")
                    else -> throw IOException("сервер: HTTP $status")
                }
            } catch (error: IOException) {
                if (cdn || (status != 0 && status != -1 && status != 200 && status !in 500..599)) throw error
            } finally {
                conn?.disconnect()
            }
        }
        throw IOException("сервис временно недоступен")
    }

    private suspend fun createRemoteProfile(subUrl: String) {
        val context = getApplication<Application>()
        val content = httpGetStringTimed(subUrl) ?: error("подписка недоступна (таймаут)")
        Libbox.checkConfig(content)
        // DEDUPE: if a profile for this same account already exists (match by sub TOKEN —
        // the device query differs), refresh it in place instead of piling up a 2nd
        // "MaestroVPN" entry that would orphan the paid one. Only a first activation creates new.
        val existing = ProfileManager.list().firstOrNull { MaestroSub.token(it.typed.remoteURL) == MaestroSub.token(subUrl) }
        if (existing != null) {
            existing.typed.remoteURL = subUrl
            File(existing.typed.path).writeText(content)
            existing.typed.lastUpdated = Date()
            ProfileManager.update(existing)
            Settings.selectedProfile = existing.id
            UpdateProfileWork.reconfigureUpdater()
            return
        }
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
        file.writeText(content)
        ProfileManager.create(profile, andSelect = true)
        UpdateProfileWork.reconfigureUpdater()
    }
}

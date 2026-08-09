package com.maestrovpn.tv.update

import androidx.annotation.StringRes
import androidx.compose.runtime.mutableStateOf
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.R
import com.maestrovpn.tv.database.Settings
import java.io.File

object UpdateState {
    private val promptCoordinator = UpdatePromptCoordinator()
    private val promptGateway = UpdatePromptGateway(
        coordinator = promptCoordinator,
        readLastShownVersion = { Settings.lastShownUpdateVersion },
        writeLastShownVersion = { Settings.lastShownUpdateVersion = it },
    )

    val hasUpdate = mutableStateOf(false)
    val updateInfo = mutableStateOf<UpdateInfo?>(null)
    internal val promptRequest = mutableStateOf<UpdatePromptRequest?>(null)
    internal val activeAttempt = mutableStateOf<UpdateAttempt?>(null)
    val isChecking = mutableStateOf(false)

    val isDownloading = mutableStateOf(false)
    val downloadProgress = mutableStateOf<Float?>(null)
    val downloadError = mutableStateOf<String?>(null)

    /**
     * What the update is ACTUALLY doing right now.
     *
     * Exists because a single «Загрузка...» label used to cover three very different waits: the
     * download itself, copying ~116 MB into the PackageInstaller session, and the up-to-4-minute
     * wait for the system «Установить?» dialog (SystemPackageInstaller.RESULT_TIMEOUT_MS). The
     * owner's report on 2026-07-30 — «постоянно загрузка» on a TCL and a Chinese TV, ending in a
     * manual install — is exactly what that looks like: the user cannot tell «still downloading»
     * from «the system is waiting for me to press a button I never saw».
     */
    enum class Phase { Idle, Downloading, Installing, AwaitingConfirm }

    val phase = mutableStateOf(Phase.Idle)

    val cachedApkFile = mutableStateOf<File?>(null)

    sealed class InstallStatus {
        data object Idle : InstallStatus()
        data object Installing : InstallStatus()
        data object Success : InstallStatus()
        data class Failed(val error: String) : InstallStatus()
    }

    val installStatus = mutableStateOf<InstallStatus>(InstallStatus.Idle)

    // System-installer confirm dialog the user has not answered yet. Android silently drops
    // startActivity from the background (worker-committed installs), so the receiver parks the
    // confirm Intent here and MainActivity re-fires it on the next foreground — the install
    // then completes IN PLACE instead of looping another download cycle.
    val pendingConfirmIntent = mutableStateOf<android.content.Intent?>(null)

    fun setUpdate(info: UpdateInfo?) {
        applyUpdateCheckResult(Result.success(info))
    }

    internal fun applyUpdateCheckResult(result: Result<UpdateInfo?>) {
        promptCoordinator.applyUpdateCheckResult(result)
        syncPromptState()
    }

    internal fun requestUpdatePrompt(
        info: UpdateInfo,
        provenance: UpdatePromptProvenance,
    ): UpdatePromptRequest {
        val request = promptGateway.publishManual(info, provenance)
        syncPromptState()
        return request
    }

    internal fun applyPromptAction(availableVersion: Int, action: UpdatePromptAction): Int =
        promptGateway.applyAction(availableVersion, action)

    internal fun beginBackgroundUpdateAttempt(info: UpdateInfo): UpdateAttempt? =
        acquireUpdateAttemptSafely(
            acquire = { promptCoordinator.beginBackgroundAttempt(info) },
            project = { syncPromptState() },
            rollback = ::rollbackProjectedAttempt,
        )

    internal fun clearUpdatePrompt(sequence: Long): Boolean {
        val cleared = promptCoordinator.clearUpdatePrompt(sequence)
        syncPromptState()
        return cleared
    }

    internal fun beginUpdateAttempt(offer: UpdatePromptOffer): UpdateAttempt? =
        acquireUpdateAttemptSafely(
            acquire = { promptCoordinator.beginAttempt(offer) },
            project = { syncPromptState() },
            rollback = ::rollbackProjectedAttempt,
        )

    internal fun completeUpdateAttempt(attemptId: Long): Boolean {
        val completed = promptCoordinator.completeAttempt(attemptId)
        syncPromptState()
        return completed
    }

    internal fun cancelUpdateAttempt(attemptId: Long): Boolean {
        val cancelled = promptCoordinator.cancelAttempt(attemptId)
        syncPromptState()
        return cancelled
    }

    internal fun retryFailedUpdateAttempt(attemptId: Long): UpdatePromptRequest? {
        val request = promptCoordinator.retryFailedAttempt(attemptId)
        syncPromptState()
        return request
    }

    fun setInstallStatus(status: InstallStatus) {
        installStatus.value = status
    }

    /**
     * The label that honestly describes [phase]. One place, because the same progress dialog is
     * drawn from three call sites (MainActivity, AppSettingsScreen, Vendor's in-place install) —
     * they already drifted once, and a fourth copy of «Загрузка...» is how this bug was born.
     * The caller appends the percentage itself, and only while downloading: during the install
     * and the confirm wait there is no progress to show, and a frozen «…100%» reads as a hang.
     */
    @StringRes
    fun phaseLabelRes(p: Phase): Int = when (p) {
        Phase.Installing -> R.string.update_installing
        Phase.AwaitingConfirm -> R.string.update_awaiting_confirm
        Phase.Downloading, Phase.Idle -> R.string.downloading
    }

    fun clear() {
        promptCoordinator.clearAll()
        syncPromptState(saveCache = false)
        isDownloading.value = false
        downloadProgress.value = null
        downloadError.value = null
        installStatus.value = InstallStatus.Idle
        cachedApkFile.value = null
        pendingConfirmIntent.value = null
        phase.value = Phase.Idle
        clearCache()
    }

    fun resetDownload() {
        isDownloading.value = false
        downloadProgress.value = null
        downloadError.value = null
        phase.value = Phase.Idle
    }

    fun loadFromCache() {
        val json = Settings.cachedUpdateInfo
        if (json.isBlank()) return

        val info = UpdateInfo.fromJson(json) ?: return
        if (info.versionCode <= BuildConfig.VERSION_CODE) {
            clearCache()
            return
        }

        promptCoordinator.applyUpdateCheckResult(Result.success(info))
        syncPromptState(saveCache = false)

        val apkPath = Settings.cachedApkPath
        if (apkPath.isNotBlank()) {
            val apkFile = File(apkPath)
            if (apkFile.exists() && apkFile.length() > 0) {
                cachedApkFile.value = apkFile
            } else {
                Settings.cachedApkPath = ""
            }
        }
    }

    private fun saveToCache(info: UpdateInfo?) {
        Settings.cachedUpdateInfo = info?.toJson() ?: ""
    }

    private fun rollbackProjectedAttempt(attemptId: Long) {
        promptCoordinator.cancelAttempt(attemptId)
        syncPromptState(saveCache = false)
    }

    private fun syncPromptState(saveCache: Boolean = true) {
        val info = promptCoordinator.candidate
        updateInfo.value = info
        hasUpdate.value = info != null
        promptRequest.value = promptCoordinator.pendingRequest
        activeAttempt.value = promptCoordinator.activeAttempt
        if (saveCache) saveToCache(info)
    }

    fun saveApkPath(file: File) {
        Settings.cachedApkPath = file.absolutePath
        cachedApkFile.value = file
    }

    private fun clearCache() {
        Settings.cachedUpdateInfo = ""
        Settings.cachedApkPath = ""
    }
}

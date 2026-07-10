package com.maestrovpn.tv.update

import androidx.compose.runtime.mutableStateOf
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.database.Settings
import java.io.File

object UpdateState {
    val hasUpdate = mutableStateOf(false)
    val updateInfo = mutableStateOf<UpdateInfo?>(null)
    val isChecking = mutableStateOf(false)

    val isDownloading = mutableStateOf(false)
    val downloadProgress = mutableStateOf<Float?>(null)
    val downloadError = mutableStateOf<String?>(null)

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
        updateInfo.value = info
        hasUpdate.value = info != null
        saveToCache(info)
    }

    fun setInstallStatus(status: InstallStatus) {
        installStatus.value = status
    }

    fun clear() {
        hasUpdate.value = false
        updateInfo.value = null
        isDownloading.value = false
        downloadProgress.value = null
        downloadError.value = null
        installStatus.value = InstallStatus.Idle
        cachedApkFile.value = null
        pendingConfirmIntent.value = null
        clearCache()
    }

    fun resetDownload() {
        isDownloading.value = false
        downloadProgress.value = null
        downloadError.value = null
    }

    fun loadFromCache() {
        val json = Settings.cachedUpdateInfo
        if (json.isBlank()) return

        val info = UpdateInfo.fromJson(json) ?: return
        if (info.versionCode <= BuildConfig.VERSION_CODE) {
            clearCache()
            return
        }

        updateInfo.value = info
        hasUpdate.value = true

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

    fun saveApkPath(file: File) {
        Settings.cachedApkPath = file.absolutePath
        cachedApkFile.value = file
    }

    private fun clearCache() {
        Settings.cachedUpdateInfo = ""
        Settings.cachedApkPath = ""
        Settings.lastShownUpdateVersion = 0
    }
}

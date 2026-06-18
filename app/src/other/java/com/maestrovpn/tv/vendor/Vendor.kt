package com.maestrovpn.tv.vendor

import android.app.Activity
import android.util.Log
import androidx.camera.core.ImageAnalysis
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.R
import com.maestrovpn.tv.bg.RootClient
import com.maestrovpn.tv.compose.screen.qrscan.QRCodeCropArea
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.update.UpdateCheckException
import com.maestrovpn.tv.update.UpdateInfo
import com.maestrovpn.tv.update.UpdateSource
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.update.UpdateTrack
import com.maestrovpn.tv.update.checkFDroidUpdate
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

object Vendor : VendorInterface {
    private const val TAG = "Vendor"

    // Lives for the app lifetime — drives the manual-update download off the UI thread.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    override fun checkUpdate(activity: Activity, byUser: Boolean) {
        try {
            val updateInfo = checkUpdateAsync()
            if (updateInfo != null) {
                activity.runOnUiThread {
                    showUpdateDialog(activity, updateInfo)
                }
            } else if (byUser) {
                activity.runOnUiThread {
                    showNoUpdatesDialog(activity)
                }
            }
        } catch (e: UpdateCheckException.TrackNotSupported) {
            Log.d(TAG, "checkUpdate: track not supported")
            if (byUser) {
                activity.runOnUiThread {
                    showTrackNotSupportedDialog(activity)
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "checkUpdate: ", e)
            if (byUser) {
                activity.runOnUiThread {
                    showNoUpdatesDialog(activity)
                }
            }
        }
    }

    private fun showUpdateDialog(activity: Activity, updateInfo: UpdateInfo) {
        val message = buildString {
            append(activity.getString(R.string.new_version_available, updateInfo.versionName))
            if (!updateInfo.releaseNotes.isNullOrBlank()) {
                append("\n\n")
                append(updateInfo.releaseNotes.take(500))
                if (updateInfo.releaseNotes.length > 500) {
                    append("...")
                }
            }
        }

        MaterialAlertDialogBuilder(activity)
            .setTitle(R.string.check_update)
            .setMessage(message)
            .setPositiveButton(R.string.update) { _, _ ->
                startInPlaceInstall(activity, updateInfo)
            }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    /**
     * Download the APK and install it IN PLACE via the system package installer (the same
     * flow the automatic update uses) — instead of opening the release URL in a browser,
     * which on a phone just saves the APK as a file the user has to find and install by
     * hand. On a TV / privileged device this is silent; on a normal phone the system shows
     * its "обновить?" prompt, then updates the app in place.
     */
    private fun startInPlaceInstall(activity: Activity, updateInfo: UpdateInfo) {
        val progress = MaterialAlertDialogBuilder(activity)
            .setTitle(R.string.update)
            .setMessage("Загрузка обновления…")
            .setCancelable(false)
            .create()
        runCatching { progress.show() }
        scope.launch {
            val ticker = launch {
                while (isActive) {
                    UpdateState.downloadProgress.value?.let { p ->
                        runCatching { progress.setMessage("Загрузка обновления… ${(p * 100).toInt()}%") }
                    }
                    delay(400)
                }
            }
            try {
                withContext(Dispatchers.IO) {
                    downloadAndInstall(activity, updateInfo.downloadUrl)
                }
                ticker.cancel()
                runCatching { progress.dismiss() }
            } catch (e: Exception) {
                Log.e(TAG, "in-place update failed", e)
                ticker.cancel()
                runCatching { progress.dismiss() }
                runCatching {
                    MaterialAlertDialogBuilder(activity)
                        .setTitle(R.string.check_update)
                        .setMessage(e.message ?: "Не удалось установить обновление")
                        .setPositiveButton(R.string.ok, null)
                        .show()
                }
            }
        }
    }

    private fun showNoUpdatesDialog(activity: Activity) {
        MaterialAlertDialogBuilder(activity)
            .setTitle(R.string.check_update)
            .setMessage(R.string.no_updates_available)
            .setPositiveButton(R.string.ok, null)
            .show()
    }

    private fun showTrackNotSupportedDialog(activity: Activity) {
        MaterialAlertDialogBuilder(activity)
            .setTitle(R.string.check_update)
            .setMessage(R.string.update_track_not_supported)
            .setPositiveButton(R.string.ok, null)
            .show()
    }

    override fun createQRCodeAnalyzer(
        onSuccess: (String) -> Unit,
        onFailure: (Exception) -> Unit,
        onCropArea: ((QRCodeCropArea?) -> Unit)?,
    ): ImageAnalysis.Analyzer? = null

    override val hasCustomUpdate = true

    override val updateSources = listOf(UpdateSource.GITHUB, UpdateSource.FDROID)

    override fun checkUpdateAsync(): UpdateInfo? {
        // Panel channel FIRST — it's the only host reachable from RU (the device
        // hits it for /sub every 15 min). GitHub/F-Droid are foreign-blocked, so
        // they're only the fallback if the panel returns nothing or errors.
        runCatching { PanelUpdateChecker().use { it.checkUpdate() } }
            .getOrNull()?.let { return it }
        return when (UpdateSource.fromString(Settings.updateSource)) {
            UpdateSource.FDROID -> checkFDroidUpdate(Application.application)
            UpdateSource.GITHUB -> {
                val track = UpdateTrack.fromString(Settings.updateTrack)
                GitHubUpdateChecker().use { checker ->
                    checker.checkUpdate(track)
                }
            }
        }
    }

    override fun scheduleAutoUpdate() {
        UpdateWorker.schedule(com.maestrovpn.tv.Application.application)
    }

    override suspend fun verifySilentInstallMethod(method: String): Boolean {
        return when (method) {
            "PACKAGE_INSTALLER" -> {
                ApkInstaller.canSystemSilentInstall()
            }
            "SHIZUKU" -> {
                if (!ShizukuInstaller.isAvailable()) {
                    return false
                }
                if (!ShizukuInstaller.checkPermission()) {
                    ShizukuInstaller.requestPermission()
                    return false
                }
                true
            }
            "ROOT" -> RootClient.checkRootAvailable()
            else -> false
        }
    }

    override suspend fun downloadAndInstall(context: android.content.Context, downloadUrl: String) {
        val cachedApk = UpdateState.cachedApkFile.value
        val apkFile = if (cachedApk != null && cachedApk.exists() && cachedApk.length() > 0) {
            cachedApk
        } else {
            ApkDownloader().use { it.download(downloadUrl) }
        }
        // Integrity check: the panel channel publishes a sha256 in update.json.
        // Verify the downloaded bytes before installing so a corrupt/MITM'd APK is
        // never handed to the package installer. GitHub releases carry no hash → "".
        val want = UpdateState.updateInfo.value?.sha256
        if (!want.isNullOrBlank()) {
            val md = java.security.MessageDigest.getInstance("SHA-256")
            apkFile.inputStream().use { ins ->
                val buf = ByteArray(8192)
                while (true) {
                    val n = ins.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                }
            }
            val got = md.digest().joinToString("") { "%02x".format(it) }
            if (!got.equals(want, ignoreCase = true)) {
                apkFile.delete()
                throw java.io.IOException("Контрольная сумма обновления не совпала — установка отменена")
            }
        }
        ApkInstaller.install(context, apkFile)
    }
}

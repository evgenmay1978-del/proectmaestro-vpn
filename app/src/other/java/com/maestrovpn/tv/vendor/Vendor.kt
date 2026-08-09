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
import com.maestrovpn.tv.update.UpdatePromptProvenance
import com.maestrovpn.tv.update.UpdateSource
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.update.UpdateTrack
import com.maestrovpn.tv.update.checkFDroidUpdate

object Vendor : VendorInterface {
    private const val TAG = "Vendor"

    override fun checkUpdate(activity: Activity, byUser: Boolean) {
        try {
            val updateInfo = checkUpdateAsync()
            if (updateInfo != null) {
                activity.runOnUiThread {
                    if (byUser) {
                        UpdateState.requestUpdatePrompt(
                            updateInfo,
                            UpdatePromptProvenance.VendorManual,
                        )
                    } else {
                        UpdateState.applyUpdateCheckResult(Result.success(updateInfo))
                    }
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
        // ApkDownloader resumes (HTTP Range), retries on slow/dropped RU links, sets
        // timeouts, and verifies size + sha256 (from the panel manifest) BEFORE returning —
        // so only a complete, intact APK reaches the installer. Always route through it
        // instead of reusing a possibly-partial cached file.
        val apkFile = ApkDownloader().use { it.download(downloadUrl) }
        ApkInstaller.install(context, apkFile)
    }
}

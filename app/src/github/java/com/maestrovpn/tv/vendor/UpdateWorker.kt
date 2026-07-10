package com.maestrovpn.tv.vendor

import android.content.Context
import android.util.Log
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.update.UpdateSource
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.update.UpdateTrack
import com.maestrovpn.tv.update.checkFDroidUpdate
import java.util.concurrent.TimeUnit

class UpdateWorker(private val appContext: Context, params: WorkerParameters) : CoroutineWorker(appContext, params) {

    companion object {
        private const val WORK_NAME = "AutoUpdate"
        private const val TAG = "UpdateWorker"

        // After this many failed installs of the SAME versionCode the background worker stops
        // retrying it (manual install stays available; a newer release resets the counter).
        private const val MAX_INSTALL_FAILURES = 3

        fun schedule(context: Context) {
            if (!Settings.autoUpdateEnabled) {
                WorkManager.getInstance(context).cancelUniqueWork(WORK_NAME)
                Log.d(TAG, "Auto update disabled, cancelled scheduled work")
                return
            }

            val constraints = Constraints.Builder()
                .setRequiredNetworkType(NetworkType.CONNECTED)
                .setRequiresBatteryNotLow(true)
                .build()

            val workRequest = PeriodicWorkRequestBuilder<UpdateWorker>(
                6,
                TimeUnit.HOURS,
            )
                .setConstraints(constraints)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 1, TimeUnit.HOURS)
                .build()

            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.UPDATE,
                workRequest,
            )
            Log.d(TAG, "Auto update scheduled")
        }
    }

    override suspend fun doWork(): Result {
        if (!Settings.autoUpdateEnabled) {
            Log.d(TAG, "Auto update disabled, skipping")
            return Result.success()
        }

        Log.d(TAG, "Checking for updates...")

        return try {
            // Panel channel FIRST (RU-reachable); GitHub/F-Droid only as fallback.
            val updateInfo = runCatching { PanelUpdateChecker().use { it.checkUpdate() } }.getOrNull()
                ?: when (UpdateSource.fromString(Settings.updateSource)) {
                    UpdateSource.FDROID -> checkFDroidUpdate(appContext)
                    UpdateSource.GITHUB -> {
                        val track = UpdateTrack.fromString(Settings.updateTrack)
                        GitHubUpdateChecker().use { it.checkUpdate(track) }
                    }
                }

            if (updateInfo == null) {
                Log.d(TAG, "No update available")
                return Result.success()
            }

            Log.d(TAG, "Update available: ${updateInfo.versionName}")
            UpdateState.setUpdate(updateInfo)

            // A different (newer) version than the one that kept failing → give it a clean slate.
            if (Settings.updateFailedVersionCode != 0 &&
                updateInfo.versionCode > 0 &&
                updateInfo.versionCode != Settings.updateFailedVersionCode
            ) {
                Settings.clearUpdateInstallFailures()
            }

            // Anti-loop damper: installing this exact version already failed MAX_INSTALL_FAILURES
            // times (signature mismatch, downgrade-in-disguise, unconfirmed dialog…). Retrying in
            // the background can only burn traffic — before this gate the fleet re-downloaded the
            // ~90 MB APK on every 6h cycle forever. Leave the offer visible in the UI; a manual
            // «Обновить» (with its visible error) is still allowed.
            if (updateInfo.versionCode > 0 &&
                updateInfo.versionCode == Settings.updateFailedVersionCode &&
                Settings.updateFailedCount >= MAX_INSTALL_FAILURES
            ) {
                Log.w(
                    TAG,
                    "Skipping auto-install of ${updateInfo.versionName}: " +
                        "${Settings.updateFailedCount} failed attempts — waiting for a newer build or manual install",
                )
                return Result.success()
            }

            if (Settings.silentInstallEnabled && ApkInstaller.canSilentInstall()) {
                Log.d(TAG, "Downloading update...")
                val apkFile = ApkDownloader().use { it.download(updateInfo.downloadUrl) }

                Log.d(TAG, "Installing update...")
                // On success the system replaces our process, so this call normally never
                // returns; on failure it now THROWS the real installer verdict (the old
                // fire-and-forget commit logged "installed successfully" no matter what).
                ApkInstaller.install(appContext, apkFile)
                Log.d(TAG, "Update installed successfully")
            } else {
                Log.d(TAG, "Silent install not available, update will be shown on next app launch")
            }

            Result.success()
        } catch (e: Exception) {
            Log.e(TAG, "Auto update failed", e)
            Result.retry()
        }
    }
}

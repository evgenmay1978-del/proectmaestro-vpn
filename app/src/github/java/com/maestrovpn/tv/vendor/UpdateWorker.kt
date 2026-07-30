package com.maestrovpn.tv.vendor

import android.content.Context
import android.net.ConnectivityManager
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

    /**
     * True when the active network charges by the byte. Guards the pre-download for devices that
     * cannot install silently — they are the ones that would otherwise pull ~116 MB over cellular
     * without anyone asking. Needs ACCESS_NETWORK_STATE, declared in our own manifest (it also
     * arrives via androidx.work, but depending on a library's manifest for a permission our code
     * calls directly is how it silently disappears one day). Any failure → treat as unmetered so
     * a broken query can never block updates entirely.
     */
    private fun isMeteredNetwork(): Boolean = runCatching {
        val cm = appContext.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        cm.isActiveNetworkMetered
    }.getOrDefault(false)

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

            val canSilent = Settings.silentInstallEnabled && ApkInstaller.canSilentInstall()

            // PRE-DOWNLOAD ALWAYS — not only when we can install silently.
            //
            // The old gate skipped the download whenever silent install was unavailable, which on
            // Android < 12 is every single device. The update then first appeared when the user
            // opened the app, and only THEN did ~116 MB start downloading, with them watching it
            // under a «Загрузка...» label. That is what the owner reported on 2026-07-30 as an
            // endless one on a TCL and a Chinese TV. With the APK already on disk, ApkDownloader's
            // verified-file fast path makes pressing «Обновить» effectively instant.
            if (!canSilent && isMeteredNetwork()) {
                // 116 MB over cellular that nobody asked for is not a favour. TVs sit on Ethernet
                // or Wi-Fi (unmetered) and are unaffected; a phone simply waits for Wi-Fi. Devices
                // that CAN install silently keep their old behaviour — unchanged on purpose.
                Log.d(TAG, "Metered network and no silent install — deferring pre-download")
                return Result.success()
            }

            Log.d(TAG, "Downloading update...")
            val apkFile = ApkDownloader().use { it.download(updateInfo.downloadUrl) }

            if (canSilent) {
                Log.d(TAG, "Installing update...")
                // On success the system replaces our process, so this call normally never
                // returns; on failure it now THROWS the real installer verdict (the old
                // fire-and-forget commit logged "installed successfully" no matter what).
                ApkInstaller.install(appContext, apkFile)
                Log.d(TAG, "Update installed successfully")
            } else {
                // Deliberately NOT attempting the install here: the system confirm dialog cannot
                // be raised from a background worker (Android 10+ drops startActivity), so
                // committing a session would only pile up sessions and burn the failure damper.
                // The APK is cached; the in-app prompt installs it in the foreground, where the
                // confirm dialog actually reaches the user.
                Log.d(TAG, "Silent install unavailable — APK pre-downloaded for the next app launch")
                UpdateTelemetry.emit(
                    "predownloaded",
                    "target=${updateInfo.versionCode} bytes=${apkFile.length()} free=${UpdateTelemetry.freeMb()}MB",
                )
            }

            Result.success()
        } catch (e: Exception) {
            Log.e(TAG, "Auto update failed", e)
            Result.retry()
        }
    }
}

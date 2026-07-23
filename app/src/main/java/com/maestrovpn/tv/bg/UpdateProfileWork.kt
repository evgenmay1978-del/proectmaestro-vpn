package com.maestrovpn.tv.bg

import android.content.Context
import android.util.Log
import androidx.work.BackoffPolicy
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequest
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.TypedProfile
import com.maestrovpn.tv.utils.httpGetStringTimed
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Date
import java.util.concurrent.TimeUnit

class UpdateProfileWork {
    companion object {
        private const val WORK_NAME = "UpdateProfile"
        private const val TAG = "UpdateProfileWork"
        // Trusted panel origin for the SILENT background auto-updater (SSRF guard).
        private const val TRUSTED_HOST = "wapmixx.ru"

        /**
         * SSRF trust boundary — is [host] the MaestroVPN subscription panel (or a subdomain)?
         * SINGLE source of truth: both the silent auto-updater below AND the QR-scanner import
         * whitelist (QRScanViewModel.processQRCode) gate on this, so a scanned or stored profile
         * can only ever auto-fetch from our own panel — never an attacker-controlled origin.
         */
        fun isTrustedSubHost(host: String?): Boolean =
            host != null && (host == TRUSTED_HOST || host.endsWith(".$TRUSTED_HOST"))

        suspend fun reconfigureUpdater() {
            runCatching {
                reconfigureUpdater0()
            }.onFailure {
                Log.e(TAG, "reconfigureUpdater", it)
            }
        }

        /**
         * Force an IMMEDIATE /sub re-fetch of the trusted remote profiles, BYPASSING the
         * autoUpdateInterval guard (unlike the periodic worker, which floors at the Android
         * 15-min minimum). Called on app foreground + on connect so a change — expiry/renewal,
         * device-cap, or the olcRTC outbound appearing/leaving — is reflected the instant the
         * user acts, not only on the next 15-min tick. Same SSRF guard + bounded fetch +
         * checkConfig + hot-reload-if-selected as the worker; a failed fetch keeps the live
         * config untouched (never disturbs the running tunnel).
         *
         * Truncate-then-write can be observed torn by a concurrent serviceReload (it reads the
         * profile file bare, no checkConfig) — a torn read stops a live tunnel with an error, so
         * all writers go through [writeAtomic]: every reader sees old or new, never a mix.
         */
        private fun writeAtomic(file: File, content: String) {
            val tmp = File(file.parentFile, file.name + ".tmp")
            tmp.writeText(content)
            if (!tmp.renameTo(file)) { // some vendor ROMs fail renameTo → degrade to the old path
                file.writeText(content)
                tmp.delete()
            }
        }

        suspend fun refreshNow() = withContext(NonCancellable) {
            // NonCancellable: the caller is the Activity's lifecycleScope — the user backing out
            // of the app must not abort the pass between "config written to disk" and "tunnel
            // reloaded", or the tunnel stays on the OLD config forever (disk==fetched → every
            // future pass sees "no change" and never reloads).
            runCatching {
                val remoteProfiles = ProfileManager.list()
                    .filter { it.typed.type == TypedProfile.Type.Remote && it.typed.autoUpdate }
                val selectedProfile = Settings.selectedProfile
                var selectedUpdated = false
                for (profile in remoteProfiles) {
                    val host = runCatching { java.net.URI(profile.typed.remoteURL).host }.getOrNull()
                    if (!isTrustedSubHost(host)) continue
                    val content = httpGetStringTimed(profile.typed.remoteURL, 6_000) ?: continue
                    try {
                        Libbox.checkConfig(content)
                    } catch (_: Exception) {
                        continue // bad config → leave the live one in place
                    }
                    val file = File(profile.typed.path)
                    // Flag BEFORE the suspend DB update + guard per profile: if a later profile
                    // throws after this one already hit disk, the reload below must still fire.
                    runCatching {
                        if (file.readText() != content) {
                            if (profile.id == selectedProfile) selectedUpdated = true
                            writeAtomic(file, content)
                            profile.typed.lastUpdated = Date()
                            ProfileManager.update(profile)
                        }
                    }.onFailure { Log.e(TAG, "refreshNow: profile ${profile.name}", it) }
                }
                if (selectedUpdated) {
                    runCatching { Libbox.newStandaloneCommandClient().serviceReload() }
                }
            }.onFailure { Log.e(TAG, "refreshNow", it) }
        }

        private suspend fun reconfigureUpdater0() {
            val remoteProfiles =
                ProfileManager.list()
                    .filter { it.typed.type == TypedProfile.Type.Remote && it.typed.autoUpdate }
            if (remoteProfiles.isEmpty()) {
                WorkManager.getInstance(Application.application).cancelUniqueWork(WORK_NAME)
                return
            }

            var minDelay =
                remoteProfiles.minByOrNull { it.typed.autoUpdateInterval }!!.typed.autoUpdateInterval.toLong()
            val nowSeconds = System.currentTimeMillis() / 1000L
            val minInitDelay =
                remoteProfiles.minOf { (it.typed.autoUpdateInterval * 60) - (nowSeconds - (it.typed.lastUpdated.time / 1000L)) }
            if (minDelay < 15) minDelay = 15
            WorkManager.getInstance(Application.application).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.UPDATE,
                PeriodicWorkRequest.Builder(UpdateTask::class.java, minDelay, TimeUnit.MINUTES)
                    .apply {
                        if (minInitDelay > 0) setInitialDelay(minInitDelay, TimeUnit.SECONDS)
                        setBackoffCriteria(BackoffPolicy.LINEAR, 15, TimeUnit.MINUTES)
                    }
                    .build(),
            )
        }
    }

    class UpdateTask(appContext: Context, params: WorkerParameters) : CoroutineWorker(appContext, params) {
        override suspend fun doWork(): Result {
            var selectedProfileUpdated = false
            val remoteProfiles =
                ProfileManager.list()
                    .filter { it.typed.type == TypedProfile.Type.Remote && it.typed.autoUpdate }
            if (remoteProfiles.isEmpty()) return Result.success()
            var success = true
            val selectedProfile = Settings.selectedProfile
            for (profile in remoteProfiles) {
                val lastSeconds =
                    (System.currentTimeMillis() - profile.typed.lastUpdated.time) / 1000L
                if (lastSeconds < profile.typed.autoUpdateInterval * 60) {
                    continue
                }
                // SSRF guard: the SILENT background updater only fetches from our trusted
                // panel host. A profile whose remoteURL points anywhere else is skipped here
                // (the explicit QR-import / buy path stays as-is). Never disturb the live tunnel
                // by talking to an untrusted origin on a timer.
                val host = runCatching { java.net.URI(profile.typed.remoteURL).host }.getOrNull()
                if (!isTrustedSubHost(host)) {
                    Log.w(TAG, "skip auto-update for untrusted origin host=$host profile=${profile.name}")
                    continue
                }
                try {
                    // Bounded fetch: a panel that stalls after TLS used to hang this worker
                    // (and thus the updater) forever. null = unreachable → fail this profile,
                    // Result.retry() reschedules. The config is overwritten only AFTER a good
                    // fetch + checkConfig, so a timed-out fetch never disturbs the live tunnel.
                    val content = httpGetStringTimed(profile.typed.remoteURL)
                        ?: error("sub fetch timed out / panel unreachable")
                    Libbox.checkConfig(content)
                    val file = File(profile.typed.path)
                    if (file.readText() != content) {
                        writeAtomic(file, content)
                        if (profile.id == selectedProfile) {
                            selectedProfileUpdated = true
                        }
                    }
                    profile.typed.lastUpdated = Date()
                    ProfileManager.update(profile)
                } catch (e: Exception) {
                    Log.e(TAG, "update profile ${profile.name}", e)
                    success = false
                }
            }
            if (selectedProfileUpdated) {
                runCatching {
                    Libbox.newStandaloneCommandClient().serviceReload()
                }
            }
            return if (success) {
                Result.success()
            } else {
                Result.retry()
            }
        }
    }
}

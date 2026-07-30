package com.maestrovpn.tv.vendor

import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.CrashReportManager
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * Outcome telemetry for the OTA update path — the ONE thing that was missing when a client
 * says «постоянно загрузка».
 *
 * The APK bytes are served by storage.yandexcloud.net, not our nginx, and the installer verdict
 * was never reported anywhere, so on S1 these three were indistinguishable:
 *   - the 116 MB download is merely slow,
 *   - the download/install ran out of storage,
 *   - the system «Установить?» dialog is up and nobody pressed it.
 * All three look identical to the user too (see MainActivity's single «Загрузка...» label), which
 * is exactly why the complaint kept coming back. Each stage below tells them apart.
 *
 * ⛔ Fire-and-forget BY DESIGN: emit() never suspends the caller, never retries, never throws.
 * An update must not be delayed — let alone failed — because a diagnostic POST did not land.
 */
object UpdateTelemetry {

    /** Reported as the report's `kind`; the backend caps kind at 24 bytes. */
    private const val KIND = "update"

    // Process-lifetime scope: emits outlive the coroutine that fired them (the installer commonly
    // kills our process moments later), and SupervisorJob keeps one failed POST from cancelling
    // the rest.
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    /**
     * @param stage machine-readable outcome, e.g. `download_ok`, `download_failed`,
     *              `install_failed`, `confirm_required`, `install_verdict`. Grep/jq on `.msg`.
     * @param details free-form `key=value` pairs for triage; goes into the report's `stack`.
     */
    fun emit(stage: String, details: String = "") {
        scope.launch {
            runCatching { CrashReportManager.sendEvent(KIND, stage, details) }
        }
    }

    /**
     * Free space on the app's own storage, in MB (-1 when it cannot be read). Present in every
     * event: an install needs room for the downloaded APK, the copy the PackageInstaller session
     * makes, AND the extraction — on an 8 GB TV that is the most likely silent killer, and it is
     * invisible from the server any other way.
     */
    fun freeMb(): Long = runCatching {
        Application.application.noBackupFilesDir.usableSpace / (1024L * 1024L)
    }.getOrDefault(-1L)
}

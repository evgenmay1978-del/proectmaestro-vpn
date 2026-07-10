package com.maestrovpn.tv.vendor

import android.content.Context
import android.util.Log
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.bg.BoxService
import com.maestrovpn.tv.bg.RootClient
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.update.UpdateState
import kotlinx.coroutines.delay
import java.io.File
import java.io.IOException

enum class InstallMethod {
    PACKAGE_INSTALLER,
    SHIZUKU,
    ROOT,
}

object ApkInstaller {

    private const val TAG = "ApkInstaller"

    /** @return true if the VPN service is confirmed stopped (or was never running). */
    private suspend fun stopServiceIfRunning(): Boolean {
        val commandSocket = File(Application.application.filesDir, "command.sock")
        if (!commandSocket.exists()) {
            return true
        }
        BoxService.stop()
        // Give the engine up to ~10s to release command.sock; a busy/throttled TV can take several
        // seconds to tear the tunnel down, and installing over a still-running VPN is unsafe.
        repeat(100) {
            delay(100)
            if (!commandSocket.exists()) {
                return true
            }
        }
        return false
    }

    fun getConfiguredMethod(): InstallMethod {
        // The Xposed/root auto-detect that used to force ROOT here was removed with the
        // hide-VPN module; fall back to the user's configured silent method (or the system
        // package installer). Stock TVs already took this path.
        return if (Settings.silentInstallEnabled) {
            InstallMethod.valueOf(Settings.silentInstallMethod)
        } else {
            InstallMethod.PACKAGE_INSTALLER
        }
    }

    /**
     * Validate the downloaded archive BEFORE touching the VPN or the package installer.
     * This is the anti-loop gate: if the "update" the channel served is actually the same
     * or an older build (broken manifest, stale mirror, wrong asset), installing it can
     * never advance the version — the checker would offer it again on the next pass and
     * the client would download+install forever. Reject it here, drop the bad APK and the
     * cached offer, and record the strike so the auto-updater stops retrying this version.
     */
    private fun validateArchive(context: Context, apkFile: File) {
        val pm = context.packageManager
        val info = pm.getPackageArchiveInfo(apkFile.absolutePath, 0)
        if (info == null) {
            apkFile.delete()
            throw IOException("Файл обновления повреждён — загрузка будет повторена")
        }
        if (info.packageName != context.packageName) {
            apkFile.delete()
            throw IOException("Файл обновления от другого приложения (${info.packageName}) — установка отклонена")
        }
        @Suppress("DEPRECATION")
        val archiveVersionCode = info.versionCode
        if (archiveVersionCode <= BuildConfig.VERSION_CODE) {
            apkFile.delete()
            // The channel advertised something "newer" but shipped bytes that are not —
            // clear the cached offer so the UI stops re-prompting for it.
            UpdateState.clear()
            throw IOException(
                "Загруженная версия (код $archiveVersionCode) не новее установленной " +
                    "(код ${BuildConfig.VERSION_CODE}) — обновление отменено",
            )
        }
    }

    suspend fun install(context: Context, apkFile: File, method: InstallMethod = getConfiguredMethod()) {
        // The versionCode this attempt is trying to reach, for the failure damper — the
        // manifest's claim, because that is the key the checkers re-offer the update under.
        val targetVersionCode = UpdateState.updateInfo.value?.versionCode ?: 0
        try {
            validateArchive(context, apkFile)
            if (!stopServiceIfRunning()) {
                // VPN engine still holding command.sock after the wait — do NOT install blindly over a
                // live tunnel; abort and let the caller retry on the next cycle.
                Log.w(TAG, "VPN service still running after stop timeout; aborting install")
                throw IOException("VPN service still running; install aborted")
            }
            when (method) {
                InstallMethod.SHIZUKU -> ShizukuInstaller.install(apkFile)
                InstallMethod.ROOT -> RootInstaller.install(apkFile)
                InstallMethod.PACKAGE_INSTALLER -> SystemPackageInstaller.install(context, apkFile)
            }
            // Reached only when the process survives the install (rare: system normally kills
            // us on success) — but if we're here, the attempt went through cleanly.
            Settings.clearUpdateInstallFailures()
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            runCatching { Settings.recordUpdateInstallFailure(targetVersionCode) }
            throw e
        }
    }

    fun canSystemSilentInstall(): Boolean = SystemPackageInstaller.canSystemSilentInstall()

    suspend fun canSilentInstall(): Boolean {
        val method = getConfiguredMethod()
        return when (method) {
            InstallMethod.PACKAGE_INSTALLER -> canSystemSilentInstall()
            InstallMethod.SHIZUKU -> ShizukuInstaller.isAvailable() && ShizukuInstaller.checkPermission()
            InstallMethod.ROOT -> RootClient.checkRootAvailable()
        }
    }
}

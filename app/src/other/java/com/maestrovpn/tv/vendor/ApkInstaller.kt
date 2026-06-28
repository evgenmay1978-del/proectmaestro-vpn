package com.maestrovpn.tv.vendor

import android.content.Context
import android.util.Log
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.BoxService
import com.maestrovpn.tv.bg.RootClient
import com.maestrovpn.tv.database.Settings
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

    suspend fun install(context: Context, apkFile: File, method: InstallMethod = getConfiguredMethod()) {
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

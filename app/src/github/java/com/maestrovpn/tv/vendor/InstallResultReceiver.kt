package com.maestrovpn.tv.vendor

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.util.Log
import com.maestrovpn.tv.update.UpdateState

class InstallResultReceiver : BroadcastReceiver() {
    companion object {
        const val ACTION_INSTALL_COMPLETE = "com.maestrovpn.tv.INSTALL_COMPLETE"
        private const val TAG = "InstallResultReceiver"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_INSTALL_COMPLETE) return

        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
        val sessionId = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, -1)

        Log.d(TAG, "Install result: session=$sessionId status=$status, message=$message")

        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                // NOT terminal — hand the system confirm dialog to the user and keep the
                // SystemPackageInstaller waiter armed; the real verdict arrives in a second
                // broadcast after the user answers (or the waiter times out readably).
                val confirmIntent = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.TIRAMISU) {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                } else {
                    @Suppress("DEPRECATION")
                    intent.getParcelableExtra(Intent.EXTRA_INTENT)
                }
                confirmIntent?.let {
                    it.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    // Background-activity-start restrictions (Android 10+) silently drop this
                    // when we're not foreground; the waiter's timeout then reports it instead
                    // of the old behavior — pretending the install succeeded.
                    runCatching { context.startActivity(it) }
                        .onFailure { e -> Log.w(TAG, "confirm intent blocked", e) }
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                Log.d(TAG, "Installation successful")
                UpdateState.setInstallStatus(UpdateState.InstallStatus.Success)
                SystemPackageInstaller.onInstallResult(sessionId, status, message)
            }
            else -> {
                Log.e(TAG, "Installation failed: $status - $message")
                UpdateState.setInstallStatus(UpdateState.InstallStatus.Failed(message ?: "Unknown error"))
                SystemPackageInstaller.onInstallResult(sessionId, status, message)
            }
        }
    }
}

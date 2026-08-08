package com.maestrovpn.tv.vendor

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.util.Log
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ProcessLifecycleOwner
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.utils.AppLifecycleObserver

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
                    // Set the visible state before the one-shot delivery decision.
                    UpdateState.phase.value = UpdateState.Phase.AwaitingConfirm
                    val appInForeground = AppLifecycleObserver.isForeground.value &&
                        ProcessLifecycleOwner.get().lifecycle.currentState
                            .isAtLeast(Lifecycle.State.STARTED)
                    val delivery = deliverInstallConfirmation(
                        confirmation = it,
                        appInForeground = appInForeground,
                        launch = { confirmation -> context.startActivity(confirmation) },
                        park = { confirmation -> UpdateState.pendingConfirmIntent.value = confirmation },
                    )
                    UpdateTelemetry.emit("confirm_required", "session=$sessionId delivery=$delivery")
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                Log.d(TAG, "Installation successful")
                UpdateState.pendingConfirmIntent.value = null
                UpdateState.setInstallStatus(UpdateState.InstallStatus.Success)
                SystemPackageInstaller.onInstallResult(sessionId, status, message)
            }
            else -> {
                Log.e(TAG, "Installation failed: $status - $message")
                // Raw PackageInstaller.STATUS_* — the only place it survives; ApkInstaller's
                // install_failed carries the human-readable mapping, this carries the code.
                UpdateTelemetry.emit("install_verdict", "session=$sessionId status=$status msg=$message")
                UpdateState.phase.value = UpdateState.Phase.Idle
                UpdateState.pendingConfirmIntent.value = null
                UpdateState.setInstallStatus(UpdateState.InstallStatus.Failed(message ?: "Unknown error"))
                SystemPackageInstaller.onInstallResult(sessionId, status, message)
            }
        }
    }
}

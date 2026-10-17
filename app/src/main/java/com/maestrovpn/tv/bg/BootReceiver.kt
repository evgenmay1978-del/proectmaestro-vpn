package com.maestrovpn.tv.bg

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.maestrovpn.tv.database.Settings
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class BootReceiver : BroadcastReceiver() {
    @OptIn(DelicateCoroutinesApi::class)
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> {
            }

            else -> return
        }
        GlobalScope.launch(Dispatchers.IO) {
            if (Settings.startedByUser) {
                CrashReportManager.refresh()
                // Skip autostart only while the app is actually crash-looping, and keep the
                // user's intent: once the loop window passes, the next boot/update connects again.
                // Previously ANY unread report blocked it and cleared startedByUser forever — but
                // nothing in the app marks reports read (there is no crash viewer screen), so a
                // single crash permanently disabled reconnect after reboot and after OTA updates.
                if (CrashReportManager.isCrashLooping()) return@launch
                withContext(Dispatchers.Main) {
                    BoxService.start()
                }
            }
        }
    }
}

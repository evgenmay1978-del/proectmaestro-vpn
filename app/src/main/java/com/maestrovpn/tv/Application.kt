package com.maestrovpn.tv

import android.app.Application
import android.app.NotificationManager
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.wifi.WifiManager
import android.os.PowerManager
import android.util.Log
import androidx.core.content.getSystemService
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.SetupOptions
import com.maestrovpn.tv.bg.AppChangeReceiver
import com.maestrovpn.tv.bg.CrashReportManager
import com.maestrovpn.tv.bg.OOMReportManager
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.constant.Bugs
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.AppLifecycleObserver
import com.maestrovpn.tv.utils.MaestroSub
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import java.io.File
import java.util.Locale
import com.maestrovpn.tv.Application as BoxApplication

class Application : Application() {
    override fun attachBaseContext(base: Context?) {
        super.attachBaseContext(base)
        application = this
    }

    override fun onCreate() {
        super.onCreate()
        AppLifecycleObserver.register(this)

//        Seq.setContext(this)
        runCatching {
            Libbox.setLocale(Locale.getDefault().toLanguageTag().replace("-", "_"))
        }.onFailure {
            Log.d("Application", "set locale: ${it.message}")
        }
        val baseDir = filesDir
        baseDir.mkdirs()
        val workingDir = getExternalFilesDir(null)
        val tempDir = cacheDir
        tempDir.mkdirs()
        if (workingDir != null) {
            workingDir.mkdirs()
            CrashReportManager.install(workingDir, baseDir)
            OOMReportManager.install(workingDir)
        }

        @Suppress("OPT_IN_USAGE")
        GlobalScope.launch(Dispatchers.IO) {
            initialize(baseDir, workingDir, tempDir)
            // Backfill the per-install device id onto an EXISTING MaestroVPN subscription so
            // the account's device cap (enforced server-side at /sub) starts applying to
            // already-installed apps right after this update — not only to freshly claimed
            // ones. Idempotent: withDevice() no-ops once the param is present.
            runCatching {
                for (p in ProfileManager.list()) {
                    val url = p.typed.remoteURL
                    if (p.name == "MaestroVPN" && url.contains("/sub/") && !url.contains("device=")) {
                        p.typed.remoteURL = MaestroSub.withDevice(this@Application, url)
                        ProfileManager.update(p)
                    }
                }
            }.onFailure { Log.d("Application", "device-id migration: ${it.message}") }
            UpdateProfileWork.reconfigureUpdater()
            // Silently ship any locally-recorded crash reports to the panel's /report sink so the
            // fleet's real failures land on S1 — proactive, no waiting for a customer to complain.
            // Off the cold-start path, best-effort; never throws.
            runCatching { CrashReportManager.uploadPending() }
        }

        if (Vendor.isPerAppProxyAvailable()) {
            registerReceiver(
                AppChangeReceiver(),
                IntentFilter().apply {
                    addAction(Intent.ACTION_PACKAGE_ADDED)
                    addAction(Intent.ACTION_PACKAGE_REPLACED)
                    addDataScheme("package")
                },
            )
        }
    }

    private fun initialize(baseDir: File, workingDir: File?, tempDir: File) {
        val actualWorkingDir = workingDir ?: return
        setupLibbox(baseDir, actualWorkingDir, tempDir)
    }

    fun reloadSetupOptions() {
        val baseDir = filesDir
        val workingDir = getExternalFilesDir(null) ?: return
        val tempDir = cacheDir
        Libbox.reloadSetupOptions(createSetupOptions(baseDir, workingDir, tempDir))
    }

    private fun setupLibbox(baseDir: File, workingDir: File, tempDir: File) {
        Libbox.setup(createSetupOptions(baseDir, workingDir, tempDir))
    }

    private fun createSetupOptions(baseDir: File, workingDir: File, tempDir: File): SetupOptions = SetupOptions().also {
        it.basePath = baseDir.path
        it.workingPath = workingDir.path
        it.tempPath = tempDir.path
        it.fixAndroidStack = Bugs.fixAndroidStack
        it.logMaxLines = 100   // smaller Go log ring buffer — pure resident-RAM trim (weak 1GB TVs)
        it.debug = false       // never run libbox in debug (extra formatting/overhead) even in a debug build
        it.crashReportSource = "Application"
        it.oomKillerEnabled = Settings.oomKillerEnabled
        it.oomKillerDisabled = Settings.oomKillerDisabled
        it.oomMemoryLimit = Settings.oomMemoryLimitMB.toLong() * 1024L * 1024L
    }

    companion object {
        lateinit var application: BoxApplication
        val notification by lazy { application.getSystemService<NotificationManager>()!! }
        val connectivity by lazy { application.getSystemService<ConnectivityManager>()!! }
        val packageManager by lazy { application.packageManager }
        val powerManager by lazy { application.getSystemService<PowerManager>()!! }
        val notificationManager by lazy { application.getSystemService<NotificationManager>()!! }
        val wifiManager by lazy { application.getSystemService<WifiManager>()!! }
        val clipboard by lazy { application.getSystemService<ClipboardManager>()!! }
    }
}

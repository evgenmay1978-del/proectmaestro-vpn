package com.maestrovpn.tv.bg

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.MutableLiveData
import go.Seq
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.CommandServerHandler
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.Notification
import io.nekohasekai.libbox.OverrideOptions
import io.nekohasekai.libbox.PlatformInterface
import io.nekohasekai.libbox.SystemProxyStatus
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.MainActivity
import com.maestrovpn.tv.constant.Action
import com.maestrovpn.tv.constant.Alert
import com.maestrovpn.tv.constant.Status
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.ktx.hasPermission
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import java.io.File

class BoxService(private val service: Service, private val platformInterface: PlatformInterface) : CommandServerHandler {
    companion object {
        private const val PROFILE_UPDATE_INTERVAL = 15L * 60 * 1000 // 15 minutes in milliseconds
        private const val TAG = "BoxService"

        // Max time to wait for the in-process mieru SOCKS5 backend to come up before
        // (re)loading the sing-box config that routes through it. Normally <2s; on timeout
        // we proceed anyway (mieru is just marked unhealthy by urltest until it appears).
        private const val MIERU_READY_TIMEOUT_MS = 8000L

        fun start() {
            val intent =
                runBlocking {
                    withContext(Dispatchers.IO) {
                        Intent(Application.application, Settings.serviceClass())
                    }
                }
            ContextCompat.startForegroundService(Application.application, intent)
        }

        fun stop() {
            Application.application.sendBroadcast(
                Intent(Action.SERVICE_CLOSE).setPackage(
                    Application.application.packageName,
                ),
            )
        }
    }

    var fileDescriptor: ParcelFileDescriptor? = null

    private val status = MutableLiveData(Status.Stopped)
    private val binder = ServiceBinder(status)
    private val notification = ServiceNotification(status, service)
    private lateinit var commandServer: CommandServer
    private val mieruHelper = MieruHelper(service)
    // The active profile's sub URL when it carries a mieru outbound — kept so the mieru
    // helper can be re-established on Doze-exit (its UDP session to the server dies during
    // deep sleep, and unlike sing-box's own outbounds it isn't auto-reconnected).
    private var mieruUrl: String? = null

    private var receiverRegistered = false
    private val receiver =
        object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                when (intent.action) {
                    Action.SERVICE_CLOSE -> {
                        stopService()
                    }

                    PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED -> {
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                            serviceUpdateIdleMode()
                        }
                    }
                }
            }
        }

    private fun startCommandServer() {
        val commandServer = CommandServer(this, platformInterface)
        commandServer.start()
        this.commandServer = commandServer
    }

    private var lastProfileName = ""

    private suspend fun startService() {
        try {
            withContext(Dispatchers.Main) {
                notification.show(lastProfileName, R.string.status_starting)
            }

            val selectedProfileId = Settings.selectedProfile
            if (selectedProfileId == -1L) {
                stopAndAlert(Alert.EmptyConfiguration)
                return
            }

            val profile = ProfileManager.get(selectedProfileId)
            if (profile == null) {
                stopAndAlert(Alert.EmptyConfiguration)
                return
            }

            val content = File(profile.typed.path).readText()
            if (content.isBlank()) {
                stopAndAlert(Alert.EmptyConfiguration)
                return
            }

            lastProfileName = profile.name
            // Bring up mieru's local SOCKS5 proxy ONLY if this profile actually carries a
            // mieru outbound (skips a needless /helpers round-trip for the VLESS/Hy2/Naive-
            // only majority). Primary path is the in-process bridge embedded in libbox.aar
            // (every ABI); the exec'd libmieru.so is the fallback. Best-effort, on its own
            // thread. sing-box's mieru outbound dials 127.0.0.1:<socksPort>.
            mieruUrl = if (content.contains("\"mieru\"")) profile.typed.remoteURL else null
            mieruUrl?.let {
                mieruHelper.start(it)
                // Don't load the config until mieru's socks backend is actually listening,
                // or its outbound is probed against a dead port (see awaitReady).
                mieruHelper.awaitReady(MIERU_READY_TIMEOUT_MS)
            }
            withContext(Dispatchers.Main) {
                notification.show(lastProfileName, R.string.status_starting)
            }

            DefaultNetworkMonitor.start()

            try {
                commandServer.startOrReloadService(
                    content,
                    OverrideOptions().apply {
                        autoRedirect = Settings.autoRedirect
                        if (Vendor.isPerAppProxyAvailable() && Settings.perAppProxyEnabled) {
                            val appList = Settings.getEffectivePerAppProxyList()
                            if (Settings.getEffectivePerAppProxyMode() == Settings.PER_APP_PROXY_INCLUDE) {
                                includePackage =
                                    PlatformInterfaceWrapper.StringArray((appList + Application.application.packageName).iterator())
                            } else {
                                excludePackage =
                                    PlatformInterfaceWrapper.StringArray((appList - Application.application.packageName).iterator())
                            }
                        }
                    },
                )
            } catch (e: Exception) {
                stopAndAlert(Alert.CreateService, e.message)
                return
            }

            if (commandServer.needWIFIState()) {
                val wifiPermission =
                    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
                        android.Manifest.permission.ACCESS_FINE_LOCATION
                    } else {
                        android.Manifest.permission.ACCESS_BACKGROUND_LOCATION
                    }
                if (!service.hasPermission(wifiPermission)) {
                    stopAndAlert(Alert.RequestLocationPermission)
                    return
                }
            }

            status.postValue(Status.Started)
            withContext(Dispatchers.Main) {
                notification.show(lastProfileName, R.string.status_started)
            }
            notification.start()
        } catch (e: Exception) {
            stopAndAlert(Alert.StartService, e.message)
            return
        }
    }

    override fun serviceStop() {
        notification.close()
        status.postValue(Status.Starting)
        mieruHelper.stop() // serviceStop()/stopAndAlert() teardown paths previously
        val pfd = fileDescriptor
        if (pfd != null) {
            pfd.close()
            fileDescriptor = null
        }
        closeService()
    }

    override fun serviceReload() {
        runBlocking {
            serviceReload0()
        }
    }

    suspend fun serviceReload0() {
        val selectedProfileId = Settings.selectedProfile
        if (selectedProfileId == -1L) {
            stopAndAlert(Alert.EmptyConfiguration)
            return
        }

        val profile = ProfileManager.get(selectedProfileId)
        if (profile == null) {
            stopAndAlert(Alert.EmptyConfiguration)
            return
        }

        val content = File(profile.typed.path).readText()
        if (content.isBlank()) {
            stopAndAlert(Alert.EmptyConfiguration)
            return
        }
        lastProfileName = profile.name
        // Reload re-reads the profile after auto-key-update (UpdateProfileWork), which may
        // have ROTATED the mieru creds/server. mieru froze the old creds at start, and a
        // bare start() is a no-op while running — so rebind with stop()+start() (re-fetches
        // /helpers). stop() alone covers a profile that dropped mieru. No-op for non-mieru.
        mieruHelper.stop()
        mieruUrl = if (content.contains("\"mieru\"")) profile.typed.remoteURL else null
        mieruUrl?.let {
            mieruHelper.start(it)
            // Block until the new mieru socks backend is up BEFORE reloading sing-box, so the
            // `mieru` outbound isn't probed against a dead port on reload. The old config keeps
            // serving the other 4 protocols meanwhile; mieru is only briefly down (as it would
            // be during any creds refresh). Times out gracefully → reload proceeds regardless.
            mieruHelper.awaitReady(MIERU_READY_TIMEOUT_MS)
        }
        try {
            commandServer.startOrReloadService(
                content,
                OverrideOptions().apply {
                    autoRedirect = Settings.autoRedirect
                    if (Vendor.isPerAppProxyAvailable() && Settings.perAppProxyEnabled) {
                        val appList = Settings.getEffectivePerAppProxyList()
                        if (Settings.getEffectivePerAppProxyMode() == Settings.PER_APP_PROXY_INCLUDE) {
                            includePackage = PlatformInterfaceWrapper.StringArray((appList + Application.application.packageName).iterator())
                        } else {
                            excludePackage = PlatformInterfaceWrapper.StringArray((appList - Application.application.packageName).iterator())
                        }
                    }
                },
            )
        } catch (e: Exception) {
            stopAndAlert(Alert.CreateService, e.message)
            return
        }

        if (commandServer.needWIFIState()) {
            val wifiPermission =
                if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
                    android.Manifest.permission.ACCESS_FINE_LOCATION
                } else {
                    android.Manifest.permission.ACCESS_BACKGROUND_LOCATION
                }
            if (!service.hasPermission(wifiPermission)) {
                stopAndAlert(Alert.RequestLocationPermission)
                return
            }
        }
    }

    override fun getSystemProxyStatus(): SystemProxyStatus? {
        val status = SystemProxyStatus()
        if (service is VPNService) {
            status.available = service.systemProxyAvailable
            status.enabled = service.systemProxyEnabled
        }
        return status
    }

    override fun setSystemProxyEnabled(isEnabled: Boolean) {
        serviceReload()
    }

    @RequiresApi(Build.VERSION_CODES.M)
    private fun serviceUpdateIdleMode() {
        if (Application.powerManager.isDeviceIdleMode) {
            commandServer.pause()
        } else {
            commandServer.wake()
            // Leaving Doze: mieru's UDP session to the server died during deep sleep and,
            // unlike sing-box's own outbounds, the helper isn't auto-reconnected — so the
            // first traffic through mieru stalls until it lazily redials ("после сна
            // работает не сразу"). Re-establish it now so it's ready on unlock. Off-main;
            // no-op for non-mieru profiles.
            mieruUrl?.let { url ->
                GlobalScope.launch(Dispatchers.IO) {
                    mieruHelper.stop()
                    mieruHelper.start(url)
                }
            }
        }
    }

    @OptIn(DelicateCoroutinesApi::class)
    private fun stopService() {
        // Allow stopping from Starting too — not only Started. If the user taps the
        // power button while the tunnel is still coming up, or it got STUCK in Starting
        // (a protocol can't connect), the stop MUST still take effect. Previously this
        // returned unless status==Started, so pressing the button during Starting did
        // nothing ("иногда невозможно отключить"). Idempotent: skip only if already
        // stopping/stopped.
        if (status.value == Status.Stopping || status.value == Status.Stopped) return
        status.value = Status.Stopping
        if (receiverRegistered) {
            service.unregisterReceiver(receiver)
            receiverRegistered = false
        }
        notification.close()
        mieruHelper.stop()
        GlobalScope.launch(Dispatchers.IO) {
            val pfd = fileDescriptor
            if (pfd != null) {
                pfd.close()
                fileDescriptor = null
            }
            DefaultNetworkMonitor.stop()
            // commandServer may not be initialized yet if we're stopping mid-Starting.
            if (::commandServer.isInitialized) {
                closeService()
                commandServer.close()
            }
            Settings.startedByUser = false
            withContext(Dispatchers.Main) {
                status.value = Status.Stopped
                service.stopSelf()
            }
        }
    }

    private fun closeService() {
        runCatching {
            commandServer.closeService()
        }.onFailure {
            commandServer.setError("android: close service: ${it.message}")
        }
    }

    private suspend fun stopAndAlert(type: Alert, message: String? = null) {
        Settings.startedByUser = false
        val pfd = fileDescriptor
        if (pfd != null) {
            pfd.close()
            fileDescriptor = null
        }
        DefaultNetworkMonitor.stop()
        mieruHelper.stop() // never leaked the mieru process/thread on the error path
        if (::commandServer.isInitialized) {
            closeService()
            commandServer.close()
        }
        withContext(Dispatchers.Main) {
            if (receiverRegistered) {
                service.unregisterReceiver(receiver)
                receiverRegistered = false
            }
            notification.close()
            binder.broadcast { callback ->
                callback.onServiceAlert(type.ordinal, message)
            }
            status.value = Status.Stopped
            service.stopSelf()
        }
    }

    @OptIn(DelicateCoroutinesApi::class)
    @Suppress("SameReturnValue")
    internal fun onStartCommand(): Int {
        // START_STICKY: if the OS kills this foreground VPN service under memory pressure,
        // have the system recreate it. Safe here because onStartCommand reads everything it
        // needs from Settings / the saved profile and NEVER from the Intent (the restart
        // delivers a null Intent), so there is no zombie / false-connected state — it simply
        // re-establishes the tunnel. Closes the one survivability gap of the old
        // START_NOT_STICKY (a low-memory mid-session kill stayed dead until reopen); reboots
        // are already covered by BootReceiver.
        if (status.value != Status.Stopped) return Service.START_STICKY
        status.value = Status.Starting

        if (!receiverRegistered) {
            ContextCompat.registerReceiver(
                service,
                receiver,
                IntentFilter().apply {
                    addAction(Action.SERVICE_CLOSE)
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                        addAction(PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED)
                    }
                },
                ContextCompat.RECEIVER_NOT_EXPORTED,
            )
            receiverRegistered = true
        }

        GlobalScope.launch(Dispatchers.IO) {
            Settings.startedByUser = true
            try {
                startCommandServer()
                // startService() promotes to foreground (ServiceNotification.show ->
                // startForeground). A system-initiated START_STICKY restart runs in the
                // BACKGROUND, where Android 12+ can refuse that promotion
                // (ForegroundServiceStartNotAllowedException). A VPN can't run without
                // foreground, so any failure here must stop CLEANLY — never crash the
                // process. The user re-launches to resume (no worse than pre-sticky).
                startService()
            } catch (e: Exception) {
                stopAndAlert(Alert.StartCommandServer, e.message)
                return@launch
            }
        }
        return Service.START_STICKY
    }

    internal fun onBind(): IBinder = binder

    internal fun onDestroy() {
        binder.close()
    }

    internal fun onRevoke() {
        stopService()
    }

    internal fun sendNotification(notification: Notification) {
        val builder =
            NotificationCompat.Builder(service, notification.identifier).setShowWhen(false)
                .setContentTitle(notification.title).setContentText(notification.body)
                .setOnlyAlertOnce(true).setSmallIcon(R.drawable.ic_stat_fox)
                .setCategory(NotificationCompat.CATEGORY_EVENT)
                .setPriority(NotificationCompat.PRIORITY_HIGH).setAutoCancel(true)
        if (!notification.subtitle.isNullOrBlank()) {
            builder.setContentInfo(notification.subtitle)
        }
        if (!notification.openURL.isNullOrBlank()) {
            builder.setContentIntent(
                PendingIntent.getActivity(
                    service,
                    0,
                    Intent(
                        service,
                        MainActivity::class.java,
                    ).apply {
                        setAction(Action.OPEN_URL).setData(Uri.parse(notification.openURL))
                        setFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
                    },
                    ServiceNotification.flags,
                ),
            )
        }
        GlobalScope.launch(Dispatchers.Main) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                Application.notification.createNotificationChannel(
                    NotificationChannel(
                        notification.identifier,
                        notification.typeName,
                        NotificationManager.IMPORTANCE_HIGH,
                    ),
                )
            }
            Application.notification.notify(notification.typeID, builder.build())
        }
    }

    override fun triggerNativeCrash() {
        Thread {
            Thread.sleep(200)
            throw RuntimeException("debug native crash")
        }.start()
    }

    override fun writeDebugMessage(message: String?) {
        Log.d("sing-box", message!!)
    }

    override fun connectSSHAgent(): Int = -1
}

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
import androidx.core.app.ServiceCompat
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
import kotlinx.coroutines.withTimeoutOrNull
import java.io.File

class BoxService(private val service: Service, private val platformInterface: PlatformInterface) : CommandServerHandler {
    companion object {
        private const val PROFILE_UPDATE_INTERVAL = 15L * 60 * 1000 // 15 minutes in milliseconds
        private const val TAG = "BoxService"

        // Hard cap on how long we wait for the native libbox close during stop. libbox holds
        // its own dup of the tun fd, so a wedged close keeps the VPN UP ("невозможно выключить,
        // VPN не гаснет"). Past this, we force the service down regardless — destroying the
        // VPNService is what makes Android tear the tun interface down.
        private const val STOP_CLOSE_TIMEOUT = 3000L

        // Same idea for reload: the native startOrReloadService can wedge; bound how long the
        // libbox command thread waits before it's freed (the reload still applies when the
        // detached job finishes). 10s tolerates a legitimately-slow rebuild.
        private const val RELOAD_TIMEOUT = 10000L

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

    private var receiverRegistered = false
    // Held for the whole tunnel lifetime so the CPU stays awake in Doze and the engine's
    // packet loop + keepalives keep running when the screen is off (screen-off "sleep" fix).
    private var wakeLock: PowerManager.WakeLock? = null
    private val wakeLockGuard = Any()
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
            acquireWakeLock()   // keep the CPU alive so the tunnel survives screen-off / Doze
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
        releaseWakeLock()
        notification.close()
        status.postValue(Status.Starting)
        val pfd = fileDescriptor
        if (pfd != null) {
            pfd.close()
            fileDescriptor = null
        }
        closeService()
    }

    @OptIn(DelicateCoroutinesApi::class)
    override fun serviceReload() {
        // serviceReload0 calls the native commandServer.startOrReloadService, which can BLOCK
        // (bad/slow config parse, in-flight urltest probe, tun reconfigure). This is a libbox-IPC
        // callback on the command-server thread, so a bare runBlocking would park that thread
        // forever and silently strand ALL future reloads (a /sub change would never apply until
        // an app restart). Run the reload on its own job and bound the wait: on timeout the
        // existing tunnel keeps serving (startOrReloadService builds-then-swaps) and the command
        // thread is freed — the reload still applies when the job finishes. Mirrors the stop fix.
        val job = GlobalScope.launch(Dispatchers.IO) {
            // runCatching is REQUIRED: serviceReload0 does a profile File.readText() (and other
            // work) OUTSIDE its own try. On this detached GlobalScope job an uncaught throw has
            // no parent to absorb it → it would reach the default handler and CRASH the process,
            // tearing down a connected user's tunnel. (The old runBlocking ran serviceReload0 on
            // the libbox command thread, where native libbox absorbed the throw.) job.join()
            // does not rethrow a launch failure, so this is the only place to catch it.
            runCatching { serviceReload0() }.onFailure { Log.e(TAG, "serviceReload0", it) }
        }
        runBlocking {
            if (withTimeoutOrNull(RELOAD_TIMEOUT) { job.join() } == null) {
                Log.w(TAG, "serviceReload: exceeded ${RELOAD_TIMEOUT}ms — kept the existing tunnel; reload applies when it completes")
            }
        }
    }

    suspend fun serviceReload0() {
        // Never reload a tunnel the user is stopping / has stopped (e.g. a periodic UpdateTask
        // reload landing mid-teardown would briefly reconnect what the user just switched off).
        if (status.value == Status.Stopping || status.value == Status.Stopped) return
        // A reload rebuilds the box → the selector reverts to "auto" (no store_selected), so an
        // olcRTC child would be orphaned (idle, no traffic). Reap it; the user re-selects olcRTC
        // to respawn. No-op when olcRTC wasn't running.
        OlcrtcManager.stop()
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
        // Keep the tunnel alive in Doze. An always-on VPN must keep passing background traffic
        // while the screen is off; the old code called commandServer.pause() on
        // ACTION_DEVICE_IDLE_MODE_CHANGED, which quiesced the engine and dropped the connection
        // until the screen came back on ("ВПН при отключённом экране засыпает"). We hold a
        // PARTIAL_WAKE_LOCK for the tunnel lifetime so the CPU keeps running — therefore never
        // pause; call wake() defensively so any prior pause is undone. Guard against the brief
        // startup window where the idle broadcast can arrive before commandServer is created.
        if (::commandServer.isInitialized) commandServer.wake()
    }

    private fun acquireWakeLock() {
        synchronized(wakeLockGuard) {
            // Stop-during-Starting race: the user can hit stop (main thread) while startService's
            // IO coroutine is still coming up — its releaseWakeLock() then runs BEFORE this
            // acquire and the lock would be held forever with the VPN off. Re-check the status
            // under the same guard releaseWakeLock uses, so a stop that already began always wins.
            if (status.value == Status.Stopping || status.value == Status.Stopped) return
            if (wakeLock != null) return
            runCatching {
                wakeLock = Application.powerManager.newWakeLock(
                    PowerManager.PARTIAL_WAKE_LOCK, "MaestroVPN:tunnel",
                ).apply {
                    setReferenceCounted(false)
                    acquire()
                }
            }.onFailure { Log.w(TAG, "acquireWakeLock failed: ${it.message}") }
        }
    }

    private fun releaseWakeLock() = synchronized(wakeLockGuard) {
        runCatching { wakeLock?.takeIf { it.isHeld }?.release() }
        wakeLock = null
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
        releaseWakeLock()
        if (receiverRegistered) {
            service.unregisterReceiver(receiver)
            receiverRegistered = false
        }
        notification.close()
        // Record the user-stop intent BEFORE the (possibly hanging) close below, so that if
        // the process is recreated by START_STICKY we don't silently reconnect a tunnel the
        // user explicitly switched off.
        Settings.startedByUser = false
        GlobalScope.launch(Dispatchers.IO) {
            // Tear down the olcRTC child (if it was running) the moment the tunnel stops, so a
            // disguise video-call process never outlives the VPN. No-op when olcRTC wasn't used.
            // Runs HERE (off the main thread): stopLocked can block up to ~2s reaping the child,
            // which froze the UI when it ran synchronously on the stop path.
            OlcrtcManager.stop()
            val pfd = fileDescriptor
            if (pfd != null) {
                runCatching { pfd.close() }
                fileDescriptor = null
            }
            DefaultNetworkMonitor.stop()
            // The native libbox close can BLOCK indefinitely (a stuck goroutine, an in-flight
            // urltest probe, gvisor/tun teardown). Because libbox holds its own dup of the tun
            // fd, the VPN stays UP until it returns — which is the "VPN не гаснет / невозможно
            // выключить" bug. So we bound it: run the close on its own job and wait at most
            // STOP_CLOSE_TIMEOUT, then force the service down regardless. commandServer may not
            // be initialized yet if we're stopping mid-Starting.
            if (::commandServer.isInitialized) {
                val closeJob = GlobalScope.launch(Dispatchers.IO) {
                    runCatching { closeService() }
                    runCatching { commandServer.close() }
                }
                if (withTimeoutOrNull(STOP_CLOSE_TIMEOUT) { closeJob.join() } == null) {
                    Log.w(TAG, "stopService: libbox close exceeded ${STOP_CLOSE_TIMEOUT}ms — forcing service down; the tun is released when the VPNService is destroyed")
                }
            }
            withContext(Dispatchers.Main) {
                status.value = Status.Stopped
                // stopForeground(REMOVE) + stopSelf() ALWAYS run, even if the close above
                // timed out — destroying the VPNService is what guarantees Android tears down
                // the tun interface, so the VPN can never get stuck "on".
                runCatching { ServiceCompat.stopForeground(service, ServiceCompat.STOP_FOREGROUND_REMOVE) }
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
        releaseWakeLock()
        val pfd = fileDescriptor
        if (pfd != null) {
            pfd.close()
            fileDescriptor = null
        }
        DefaultNetworkMonitor.stop()
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

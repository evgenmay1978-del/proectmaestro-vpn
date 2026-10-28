package com.maestrovpn.tv.bg

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Network
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
import com.maestrovpn.tv.utils.Socks5Tunnel
import com.maestrovpn.tv.utils.TunnelProbe
import com.maestrovpn.tv.whitelist.ProbeInbound
import com.maestrovpn.tv.whitelist.TunnelWatchdog
import com.maestrovpn.tv.whitelist.WhiteListConfig
import com.maestrovpn.tv.whitelist.WhiteListSelection
import com.maestrovpn.tv.whitelist.WhiteListSession
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
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

        // Stable key for the watchdog's network listener. DefaultNetworkListener REPLACES a listener
        // registered under the same key, so a service restart can never accumulate stale entries. The
        // registration itself is deliberately kept for the process lifetime: it forwards only to the
        // CURRENT host (a volatile field), so a stale registration is inert, while unregistering it
        // asynchronously would race with the next start and could silently drop the fresh one.
        private val WATCHDOG_NETWORK = Any()

        /**
         * How long "connecting" may last before the gate gives up. The probe runs within ~2 s of the
         * start and every 3-35 s after a failure, so a healthy start reports "connected" in seconds;
         * this bound only exists so that a device with no usable path cannot sit in the very same
         * endless "connecting" state this defect is about.
         */
        private const val STARTUP_GATE_MS = 30_000L

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
    private val contentMutex = Mutex()

    // D3 (audit 28.10.2026): "Started" used to be posted as soon as the engine accepted the config
    // — a REQUEST, not proof that a single byte can cross the tunnel. Every applied config now
    // carries a loopback SOCKS5 entry (ProbeInbound) and the status follows a real HTTPS request
    // through it. probeEndpoint stays null when the config cannot carry the entry or the engine
    // rejects it; the pre-D3 immediate start is kept then, because a probe we cannot run must never
    // wedge every client on "Подключение…".
    @Volatile
    private var probeEndpoint: ProbeInbound.Endpoint? = null
    private var tunnelWatchdog: TunnelWatchdog? = null
    private var watchdogGate: Job? = null

    // DefaultNetworkListener re-delivers the same Network on every capability change; only a genuinely
    // different network (or losing it) is worth a watchdog event — otherwise every capabilities update
    // would trigger a redial and reset the session.
    private var watchdogNetwork: Network? = null
    private val watchdogNetworkGuard = Any()
    private val whiteListSession = (service as? VPNService)?.let { vpn ->
        WhiteListSession(vpn) { expired, ordinaryTag ->
            val restored = ordinaryTag?.let { WhiteListSelection.restoreOrdinary(expired, it) }
            if (restored != null) {
                GlobalScope.launch(Dispatchers.IO) {
                    // Starting already consumes the replaced request in applySelectedContent.
                    if (status.value != Status.Started) return@launch
                    runCatching { reloadSelectedContent(restored) }.onFailure {
                        stopAndAlert(Alert.CreateService, "Не удалось восстановить обычное подключение")
                    }
                }
            } else {
                val version = WhiteListSelection.version()
                val shouldStop = WhiteListSelection.current() == null || WhiteListSelection.matches(expired)
                GlobalScope.launch(Dispatchers.Main) {
                    if (shouldStop && WhiteListSelection.version() == version) {
                        WhiteListSelection.clear(expired)
                        stopService()
                    }
                }
            }
        }
    }

    private var receiverRegistered = false
    // Held for the whole tunnel lifetime so the CPU stays awake in Doze and the engine's
    // packet loop + keepalives keep running when the screen is off (screen-off "sleep" fix).
    private var wakeLock: PowerManager.WakeLock? = null
    private val wakeLockGuard = Any()
    private val receiver =
        object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                when (intent.action) {
                    WhiteListSelection.ACTION -> {
                        val requested = WhiteListSelection.current() ?: return
                        GlobalScope.launch(Dispatchers.IO) {
                            if (status.value != Status.Started) return@launch
                            runCatching { reloadSelectedContent(requested) }.onFailure {
                                stopAndAlert(Alert.CreateService, "CDN: подключение недоступно")
                            }
                        }
                    }
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

    private fun buildOverrideOptions(content: String, cdn: Boolean = false): OverrideOptions =
        OverrideOptions().apply {
            autoRedirect = Settings.autoRedirect
            val appPackage = Application.application.packageName
            val perAppEnabled = Vendor.isPerAppProxyAvailable() && Settings.perAppProxyEnabled
            // The app itself never rides its own tunnel: claim / sub / OTA must survive a dead
            // config (see PackagePolicy; incident 21.10.2026).
            val policy = PackagePolicy.decide(
                appPackage = appPackage,
                perAppEnabled = perAppEnabled,
                includeMode = Settings.getEffectivePerAppProxyMode() == Settings.PER_APP_PROXY_INCLUDE,
                appList = Settings.getEffectivePerAppProxyList(),
            )
            if (policy.include.isNotEmpty()) {
                includePackage = PlatformInterfaceWrapper.StringArray(policy.include.iterator())
            }
            if (policy.exclude.isNotEmpty()) {
                excludePackage = PlatformInterfaceWrapper.StringArray(policy.exclude.iterator())
            }
        }

    /** Serialize native configuration changes; the latest explicit request wins. */
    private suspend fun applySelectedContent(content: String, sourceAccount: Pair<Long, Long>, expected: WhiteListSelection.Request? = null): Boolean = contentMutex.withLock {
        if (expected != null && !WhiteListSelection.matches(expected)) return@withLock true
        if (expected != null && whiteListSession?.ready(expected) == true && WhiteListSelection.view.value.active == expected.tag) return@withLock true
        repeat(3) {
            if (status.value == Status.Stopping || status.value == Status.Stopped) return@withLock false
            check(WhiteListSelection.account() == sourceAccount) { "Выбранный аккаунт изменился" }
            val request = WhiteListSelection.current()
            check(request == null || (request.profileId to request.revision) == sourceAccount) { "Выбранный аккаунт изменился" }
            check(request != null || !WhiteListSelection.requiresExplicitChoice()) { "CDN: выберите подключение" }
            whiteListSession?.close()
            val cdn = request?.tag?.startsWith("cdn:") == true
            try {
                val effective = WhiteListConfig.stripAwgDecoys(when {
                    cdn -> requireNotNull(whiteListSession?.prepare(content, requireNotNull(request))) { "CDN unavailable" }
                    request != null -> WhiteListConfig.selectOrdinary(content, request.tag)
                    else -> content
                })
                if (request != null && !WhiteListSelection.matches(request)) return@repeat
                check(WhiteListSelection.account() == sourceAccount) { "Выбранный аккаунт изменился" }
                val applied = attachProbeInbound(effective)
                if (cdn) {
                    // Diagnose-02.10.2026: a valid injected CDN config was never used by the engine
                    // (traffic stayed on the ordinary nodes while the UI showed CDN connected), so the
                    // engine's own verdict on the exact bytes we hand over is logged before the call.
                    val verdict = runCatching { Libbox.checkConfig(applied) }.exceptionOrNull()
                    Log.d(TAG, "cdn apply: bytes=" + applied.length + " hasCdnTag=" + applied.contains("cdn:") +
                        " checkConfig=" + (verdict?.message ?: "ok"))
                    if (verdict != null) throw IllegalStateException("CDN: конфигурация отклонена ядром", verdict)
                }
                Log.d(TAG, "startOrReloadService cdn=" + cdn + " bytes=" + applied.length)
                commandServer.startOrReloadService(applied, buildOverrideOptions(applied, cdn))
                if (status.value == Status.Stopping || status.value == Status.Stopped) {
                    whiteListSession?.close()
                    closeService()
                    return@withLock false
                }
                if (request != null && !WhiteListSelection.matches(request)) return@repeat
                check(WhiteListSelection.account() == sourceAccount) { "Выбранный аккаунт изменился" }
                if (cdn && whiteListSession?.ready(requireNotNull(request)) != true) error("CDN expired")
                if (request != null) WhiteListSelection.started(request)
                return@withLock true
            } catch (e: Exception) {
                whiteListSession?.close()
                if (request != null && !WhiteListSelection.matches(request)) return@repeat
                // D2 root, startup half: a CDN session that failed to come up (DNS budget, rejected
                // bytes, a dead edge, the native client not starting) used to stop the whole box
                // whenever cellular was present — the ordinary nodes were alive but never tried.
                // Roll back whenever a rollback target exists; only a config without ordinary nodes
                // (ordinaryTag == null) stops the tunnel.
                // The account guard stays authoritative: a switched account still aborts instead of
                // silently rolling an old account's selection back.
                if (cdn && WhiteListSelection.account() == sourceAccount) {
                    val ordinaryTag = WhiteListConfig.ordinaryTag(content)
                    if (ordinaryTag != null && WhiteListSelection.restoreOrdinary(requireNotNull(request), ordinaryTag) != null) return@repeat
                }
                // A libbox parse error could contain a fragment of the ephemeral credentials.
                if (cdn) throw IllegalStateException("CDN: подключение недоступно")
                throw e
            }
        }
        error("Selection changed")
    }

    private suspend fun reloadSelectedContent(request: WhiteListSelection.Request) {
        if (!WhiteListSelection.matches(request)) return
        val sourceAccount = WhiteListSelection.account()
        if (sourceAccount != (request.profileId to request.revision)) return
        val profile = requireNotNull(ProfileManager.get(request.profileId))
        applySelectedContent(File(profile.typed.path).readText(), sourceAccount, request)
    }

    private suspend fun startService() {
        try {
            withContext(Dispatchers.Main) {
                notification.show(lastProfileName, R.string.status_starting)
            }

            val sourceAccount = WhiteListSelection.account()
            val selectedProfileId = sourceAccount.first
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
                if (!applySelectedContent(content, sourceAccount)) return
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

            // The config is applied; what remains is PROOF that it carries traffic. Status goes to
            // Started only after a probe comes back through the tunnel — that is the D3 fix. Without
            // a probe channel the old immediate start is kept, so a device where the engine refuses
            // the extra inbound behaves exactly as before instead of hanging on "Подключение…".
            if (probeEndpoint == null) markStarted() else startTunnelWatchdog()
            // A selection received after configuration applied but while status was Starting
            // has no live command feed yet. Consume it here instead of losing that wake-up.
            WhiteListSelection.current()?.let { requested ->
                if (whiteListSession?.ready(requested) != true) reloadSelectedContent(requested)
            }
        } catch (e: Exception) {
            stopAndAlert(Alert.StartService, e.message)
            return
        }
    }

    /**
     * Adds the loopback SOCKS5 entry the liveness probe dials into. The ENGINE gets the last word:
     * if it rejects the extra inbound we hand over the untouched config and disable probing — a
     * diagnostic channel must never cost the customer a working tunnel.
     */
    private fun attachProbeInbound(config: String): String {
        val probed = ProbeInbound.attach(config)
        if (probed == null) {
            probeEndpoint = null
            return config
        }
        val defect = runCatching { Libbox.checkConfig(probed.config) }.exceptionOrNull()
        if (defect != null) {
            probeEndpoint = null
            Log.w(TAG, "probe inbound rejected by the engine: " + defect.message + " — liveness probe disabled")
            return config
        }
        probeEndpoint = probed.endpoint
        return probed.config
    }

    /**
     * The only place that may report "connected". Idempotent: the watchdog can answer long after the
     * user stopped the service, and both the probe and the stop race for this state.
     */
    private suspend fun markStarted() {
        val started = withContext(Dispatchers.Main) {
            if (status.value != Status.Starting) return@withContext false
            status.value = Status.Started
            notification.show(lastProfileName, R.string.status_started)
            true
        }
        if (!started) return
        watchdogGate?.cancel()
        watchdogGate = null
        acquireWakeLock()   // keep the CPU alive so the tunnel survives screen-off / Doze
        notification.start()
    }

    /**
     * Watches the applied config: probes every 25-35 s and, when the path dies, redials once, waits,
     * then leaves a dead CDN edge for the ordinary nodes (or stops honestly). One edge per session:
     * per-edge advance needs WhiteListSession to open a session for a different transport profile,
     * which it does not support yet — the ordinary rollback is the safer second step today.
     */
    private suspend fun startTunnelWatchdog() {
        if (probeEndpoint == null) return
        stopTunnelWatchdog()
        val host = TunnelWatchdog(
            probe = { timeoutMillis ->
                val current = probeEndpoint
                if (current == null) false else probeTunnel(current, timeoutMillis)
            },
            onLive = { GlobalScope.launch(Dispatchers.IO) { runCatching { markStarted() } } },
            onRedial = { edge -> GlobalScope.launch(Dispatchers.IO) { runCatching { redialTunnel(edge) } } },
            onFallbackOrdinary = { GlobalScope.launch(Dispatchers.IO) { runCatching { fallbackOrGiveUp() } } },
            onClearSession = { whiteListSession?.close() },
            log = { Log.d(TAG, it) },
        )
        // A fresh host must learn the current network even if it has not changed since the last one.
        synchronized(watchdogNetworkGuard) { watchdogNetwork = null }
        tunnelWatchdog = host
        host.start(1)
        DefaultNetworkListener.start(WATCHDOG_NETWORK) { network -> onWatchdogNetwork(network) }
        // Bound the "connecting" state: with no path at all the machine just waits for a network, so
        // without this timer the UI would show "connecting" forever — the symptom this work removes.
        watchdogGate = GlobalScope.launch(Dispatchers.IO) {
            delay(STARTUP_GATE_MS)
            if (status.value != Status.Starting || tunnelWatchdog == null) return@launch
            Log.w(TAG, "startup gate: no live traffic in ${STARTUP_GATE_MS / 1000}s — giving up on this start")
            watchdogGate = null   // this job is the caller: stopTunnelWatchdog must not cancel it mid-fallback
            runCatching { fallbackOrGiveUp() }
        }
    }

    /** Forwards ONLY changes: the listener also re-delivers the same network on capability updates. */
    private fun onWatchdogNetwork(network: Network?) {
        val host = tunnelWatchdog ?: return
        val changed = synchronized(watchdogNetworkGuard) {
            val previous = watchdogNetwork
            watchdogNetwork = network
            previous != network
        }
        if (!changed) return
        if (network != null) host.networkAvailable() else host.networkLost()
    }

    private fun stopTunnelWatchdog() {
        watchdogGate?.cancel()
        watchdogGate = null
        tunnelWatchdog?.stop()
        tunnelWatchdog = null
    }

    /**
     * One probe through the applied config. "connect" is retried once: the engine binds the probe
     * inbound a moment after startOrReloadService returns, and a refused connect there says nothing
     * about the tunnel itself.
     */
    private suspend fun probeTunnel(endpoint: ProbeInbound.Endpoint, timeoutMillis: Long): Boolean {
        val credentials = Socks5Tunnel.Credentials(endpoint.username, endpoint.password)
        val budget = timeoutMillis.toInt().coerceIn(1_000, TunnelProbe.TIMEOUT_MS)
        var result = TunnelProbe.probeAny(endpoint.port, credentials, timeoutMs = budget)
        if (!result.ok && result.failure == "connect") {
            delay(400)
            result = TunnelProbe.probeAny(endpoint.port, credentials, timeoutMs = minOf(budget, 3_000))
        }
        if (result.ok) {
            Log.d(TAG, "probe ok status=" + result.status + " in " + result.elapsedMs + "ms")
        } else {
            Log.d(TAG, "probe failed failure=" + result.failure + " elapsed=" + result.elapsedMs + "ms")
        }
        return result.ok
    }

    /** Cheap local redial: re-apply the current selection without bugging the user. */
    private suspend fun redialTunnel(edge: Int) {
        if (status.value != Status.Starting && status.value != Status.Started) return
        val request = WhiteListSelection.current()
        val sourceAccount = WhiteListSelection.account()
        val profile = ProfileManager.get(request?.profileId ?: sourceAccount.first) ?: return
        Log.d(TAG, "watchdog redial edge=" + edge + " profile=" + profile.id)
        applySelectedContent(File(profile.typed.path).readText(), sourceAccount, request)
    }

    /**
     * The current path is proven dead. Leave a CDN edge for the ordinary nodes when the selector has
     * them (the D2 rollback machinery), then keep watching the ordinary path; with nothing to fall
     * back to, stop honestly — a green UI over a dead tunnel is the defect being fixed. startedByUser
     * is NOT cleared: this failure is not the user's to fix, so autostart after reboot must survive.
     */
    private suspend fun fallbackOrGiveUp() {
        stopTunnelWatchdog()
        val request = WhiteListSelection.current()
        val sourceAccount = WhiteListSelection.account()
        val profile = ProfileManager.get(request?.profileId ?: sourceAccount.first)
        val content = profile?.let { runCatching { File(it.typed.path).readText() }.getOrNull() }
        if (request != null && content != null) {
            val ordinaryTag = WhiteListConfig.ordinaryTag(content)
            if (ordinaryTag != null) {
                val restored = WhiteListSelection.restoreOrdinary(request, ordinaryTag)
                if (restored != null) {
                    val applied = runCatching { applySelectedContent(content, sourceAccount, restored) }.getOrDefault(false)
                    if (applied) {
                        Log.d(TAG, "watchdog fell back to ordinary nodes")
                        startTunnelWatchdog()
                        return
                    }
                }
            }
        }
        if (status.value == Status.Starting || status.value == Status.Started) {
            stopAndAlert(Alert.CreateService, "Туннель не пропускает трафик: подключение остановлено")
        }
    }

    override fun serviceStop() {
        stopTunnelWatchdog()
        whiteListSession?.close()
        WhiteListSelection.clear()
        releaseWakeLock()
        notification.close()
        // The core stopped the box on its own (config rejected, outbound unavailable).
        // Upstream (io.nekohasekai/sfa bg/BoxService.kt:183) posts Starting here because it
        // immediately re-starts the engine; we never do, so Starting was a dead end: the UI
        // stuck on "Подключение…" forever (stopService() only exits on Stopping/Stopped, and
        // onStartCommand ignores a start while status != Stopped). Report the truth instead.
        status.postValue(Status.Stopped)
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
        val sourceAccount = WhiteListSelection.account()
        val selectedProfileId = sourceAccount.first
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
            if (!applySelectedContent(content, sourceAccount)) return
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
        stopTunnelWatchdog()
        whiteListSession?.close()
        WhiteListSelection.clear()
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
        stopTunnelWatchdog()
        whiteListSession?.close()
        WhiteListSelection.clear()
        // Only failures that need the USER to act (no profile, a missing permission) cancel
        // "reconnect after reboot". A transient start failure — network not up yet at boot, CDN
        // edge unreachable, a background restart refused by Android — used to clear the flag
        // too, so one bad start silently disabled autostart for every later reboot and update.
        if (type == Alert.EmptyConfiguration || type == Alert.RequestLocationPermission) {
            Settings.startedByUser = false
        }
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
                    addAction(WhiteListSelection.ACTION)
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
        stopTunnelWatchdog()
        whiteListSession?.destroy()
        WhiteListSelection.destroyed()
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

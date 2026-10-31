package com.maestrovpn.tv.aivpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import com.aivpn.client.AivpnJni
import com.maestrovpn.tv.R
import com.maestrovpn.tv.bg.BoxService
import com.maestrovpn.tv.compose.MainActivity
import com.maestrovpn.tv.database.Settings
import org.json.JSONObject
import java.net.InetAddress
import kotlin.concurrent.thread

/**
 * VpnService that carries the experimental AIVPN protocol.
 *
 * It is deliberately independent from [com.maestrovpn.tv.bg.VPNService]: that one owns the
 * sing-box engine and is left untouched. Android permits one VPN interface per device, so both
 * sides stop the other before asking the system for theirs (see [AivpnTunnel.stopIfActive] and
 * [BoxService.stop] below).
 *
 * The native core calls back into THIS object over JNI. The three public callbacks below are part
 * of the AIVPN ABI — names and signatures are pinned by
 * `crates/aivpn-android-core/src/android_tunnel.rs` and must not be renamed:
 *
 *  - [resolveTransportHost] — DNS through the physical network (alt-transport, hostname keys);
 *  - [configureTunnel] — called on the main path right after the handshake, before the first TUN
 *    read; hands Rust a dup of the tun fd and applies a server-assigned address/prefix/MTU;
 *  - [onTunnelReady] — handshake and key ratchet are complete; the first honest "connected".
 */
class AivpnVpnService : VpnService() {

    /**
     * Tun parameters. The interface only accepts them at [Builder.establish] time, so a changed
     * value (the server may re-home this client to another pool address or MTU) means a rebuild.
     */
    private data class TunParams(
        val address: String,
        val prefixLen: Int,
        val mtu: Int,
        val ipv6Address: String?,
        val ipv6PrefixLen: Int,
    )

    companion object {
        private const val TAG = "AivpnVpnService"
        private const val CHANNEL_ID = "aivpn"
        private const val NOTIFICATION_ID = 2
        private const val ACTION_STOP = "com.maestrovpn.tv.aivpn.STOP"
        private const val STOP_JOIN_MS = 2000L

        private const val DNS_PRIMARY = "1.1.1.1"
        private const val DNS_SECONDARY = "8.8.8.8"
        private const val MIN_PREFIX_LEN = 1
        private const val MAX_PREFIX_LEN = 30
        private const val MIN_MTU = 576
        private const val MAX_MTU = 1500

        /** mask verification: 0 = off, 1 = warn, 2 = enforce (aivpn-core contract). */
        private const val MASK_VERIFY_OFF = 0
        private const val MASK_VERIFY_WARN = 1

        /** 0 = adaptive off; aivpn-core clamps the value into 0..3. */
        private const val ADAPTIVE_LEVEL = 0
    }

    private val tunLock = Any()

    @Volatile private var engine: Thread? = null
    @Volatile private var tun: ParcelFileDescriptor? = null
    @Volatile private var tunParams: TunParams? = null

    /** True from the first TUN build until stop/failure: the guard for the JNI callbacks. */
    @Volatile private var active = false

    /**
     * Set before the native session is stopped: a callback that arrives during teardown must not
     * publish a status or touch an interface that is already closed.
     */
    @Volatile private var closing = false

    /** Set once a terminal status is published, so onDestroy() does not overwrite a failure. */
    @Volatile private var finished = false

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            shutdown()
            return START_NOT_STICKY
        }
        // The platform gives a startForegroundService() caller ~5 s; notify before anything else.
        if (!startForegroundCompat(getString(R.string.aivpn_notification_connecting))) {
            fail(getString(R.string.aivpn_error_start_failed))
            return START_NOT_STICKY
        }
        if (active) {
            Log.i(TAG, "a session is already running, ignoring the start request")
            return START_NOT_STICKY
        }
        beginSession()
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        // Another VPN app took the interface over, or the user switched ours off in the system UI.
        Log.i(TAG, "vpn permission revoked, shutting the tunnel down")
        shutdown()
        super.onRevoke()
    }

    override fun onDestroy() {
        shutdown()
        super.onDestroy()
    }

    private fun beginSession() {
        closing = false
        finished = false
        if (!AivpnJni.isAvailable) {
            fail(getString(R.string.aivpn_error_library_missing, AivpnJni.loadError.orEmpty()))
            return
        }
        val key = try {
            AivpnKey.parse(Settings.aivpnKey)
        } catch (e: AivpnKeyException) {
            fail(getString(R.string.aivpn_error_bad_key, e.message.orEmpty()))
            return
        }
        if (VpnService.prepare(this) != null) {
            fail(getString(R.string.aivpn_error_permission))
            return
        }

        // One VPN interface per device: stop the sing-box engine before asking for ours.
        BoxService.stop()

        val initial = TunParams(key.vpnIp, key.prefixLen, key.mtu, null, 0)
        val pfd = try {
            synchronized(tunLock) { establishTunLocked(initial) }
        } catch (e: IllegalArgumentException) { // e.g. "Cannot set address"
            fail(getString(R.string.aivpn_error_establish, e.message.orEmpty()))
            return
        } catch (e: IllegalStateException) {
            fail(getString(R.string.aivpn_error_establish, e.message.orEmpty()))
            return
        } catch (e: SecurityException) {
            fail(getString(R.string.aivpn_error_establish, e.message.orEmpty()))
            return
        }

        // A pending-stop flag survives a session; without clearing it the next runTunnel would
        // return immediately (same trap the reference client documents).
        runCatching { AivpnJni.clearPendingStop() }

        active = true
        AivpnTunnel.publish(AivpnStatus.Connecting)
        updateNotification(getString(R.string.aivpn_notification_connecting))

        val fd = pfd.fd
        // start = false: the thread reference must be stored before the native call can return and
        // finishSession() can clear it, otherwise a very early failure leaves a stale thread here.
        val worker = thread(name = "aivpn-tunnel", start = false) { runEngine(fd, key) }
        engine = worker
        worker.start()
    }

    /**
     * JNI callback (android_tunnel.rs:176): the server's network configuration, or "null".
     *
     * Rust calls this after the handshake and before it reads the TUN, so a server-side pool
     * re-home (another VPN IP, prefix or MTU) can still be applied. When the values did not
     * change the existing interface is reused.
     *
     * @return a dup of the current tun fd (owned and closed by Rust), or -1 when the session is
     *         no longer able to serve a tunnel.
     */
    @Suppress("unused") // called from Rust through JNI
    fun configureTunnel(networkJson: String): Int {
        if (!active || closing) {
            Log.i(TAG, "configureTunnel ignored: the session is winding down")
            return -1
        }
        val desired = if (networkJson == "null" || networkJson.isBlank()) {
            null
        } else {
            try {
                parseNetworkConfig(networkJson)
            } catch (e: Exception) {
                Log.e(TAG, "cannot apply the server network configuration", e)
                return -1
            }
        }
        return synchronized(tunLock) {
            if (!active || closing) return@synchronized -1
            val target = desired ?: tunParams
            if (target == null) {
                Log.e(TAG, "no tun parameters to hand over")
                return@synchronized -1
            }
            if (tun == null || tunParams != target) {
                Log.i(TAG, "rebuilding the tun interface: mtu=${target.mtu} address=${target.address}/${target.prefixLen}")
                try {
                    establishTunLocked(target)
                } catch (e: Exception) {
                    Log.e(TAG, "cannot rebuild the tun interface", e)
                    return@synchronized -1
                }
            }
            val pfd = tun ?: return@synchronized -1
            try {
                ParcelFileDescriptor.dup(pfd.fileDescriptor).detachFd()
            } catch (e: Exception) {
                Log.e(TAG, "cannot duplicate the tun fd", e)
                -1
            }
        }
    }

    /**
     * JNI callback (android_tunnel.rs:231): handshake and key ratchet are complete.
     *
     * Losing this callback only costs an honest status (Rust logs a warning), but until it fires
     * the tunnel is not usable, so [AivpnStatus.Running] is published here and nowhere earlier.
     */
    @Suppress("unused") // called from Rust through JNI
    fun onTunnelReady(host: String) {
        if (!active || closing) {
            Log.i(TAG, "ignoring onTunnelReady: the session is winding down")
            return
        }
        Log.i(TAG, "tunnel is ready: $host")
        AivpnTunnel.publish(AivpnStatus.Running)
        updateNotification(getString(R.string.aivpn_notification_running))
    }

    /**
     * JNI callback (android_tunnel.rs:96): DNS through the physical network.
     *
     * Once the default route points into the tunnel, the system resolver would query the tunnel's
     * own DNS; the transport must resolve its server through the real uplink instead. A probe of
     * the physical networks comes first, the system resolver is the last resort (an address is
     * better than a hard failure when the platform reports no usable network).
     */
    @Suppress("unused") // called from Rust through JNI
    fun resolveTransportHost(host: String): Array<String> {
        val manager = getSystemService(ConnectivityManager::class.java)
        if (manager != null) {
            val networks: Array<Network> = try {
                manager.allNetworks
            } catch (e: RuntimeException) {
                emptyArray()
            }
            for (network in networks) {
                if (!isPhysical(manager, network)) continue
                val addresses: Array<InetAddress>? = try {
                    network.getAllByName(host)
                } catch (e: java.net.UnknownHostException) {
                    null
                }
                val text = addresses?.mapNotNull { it.hostAddress }?.filter { it.isNotEmpty() }.orEmpty()
                if (text.isNotEmpty()) return text.toTypedArray()
            }
        }
        return InetAddress.getAllByName(host).mapNotNull { it.hostAddress }.toTypedArray()
    }

    private fun isPhysical(manager: ConnectivityManager, network: Network): Boolean {
        val capabilities = manager.getNetworkCapabilities(network) ?: return false
        return capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) &&
            capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
    }

    /** Parses the JSON the server sends in its ServerHello (ClientNetworkConfig on the Rust side). */
    private fun parseNetworkConfig(json: String): TunParams {
        val config = JSONObject(json)
        val address = config.getString("client_ip")
        val prefixLen = config.optInt("prefix_len", AivpnKey.DEFAULT_PREFIX_LEN).coerceIn(MIN_PREFIX_LEN, MAX_PREFIX_LEN)
        val mtu = config.optInt("mtu", AivpnKey.DEFAULT_MTU).coerceIn(MIN_MTU, MAX_MTU)
        // org.json turns a JSON null into the string "null", not into the fallback.
        val ipv6 = config.optString("ipv6_address", "").takeIf { it.isNotEmpty() && it != "null" }
        val ipv6PrefixLen = if (ipv6 != null) config.optInt("ipv6_prefix_len", 0) else 0
        val serverVpnIp = config.optString("server_vpn_ip", "")
        if (serverVpnIp.isNotEmpty()) Log.i(TAG, "server vpn address: $serverVpnIp")
        return TunParams(address, prefixLen, mtu, ipv6, ipv6PrefixLen)
    }

    /**
     * Builds the tun interface. Caller holds [tunLock].
     *
     * Replaces the previous interface: Android keeps one per service, and the old fd only holds a
     * dead interface open. Rust has already received a dup for the previous session and never
     * touches the fd it got in runTunnel() again (it only dup()s that as a fallback for platforms
     * whose configure_tunnel returns None — Android always returns Some).
     */
    private fun establishTunLocked(params: TunParams): ParcelFileDescriptor {
        runCatching { tun?.close() }
        tun = null
        tunParams = null
        val builder = VpnService.Builder()
            .setSession("MaestroVPN AIVPN")
            .addAddress(params.address, params.prefixLen)
            .setMtu(params.mtu)
            // The Rust core drives the tun fd with epoll/AsyncFd; a blocking fd would stall it.
            .setBlocking(false)
            .addRoute("0.0.0.0", 0)
            .addDnsServer(DNS_PRIMARY)
            .addDnsServer(DNS_SECONDARY)
        if (params.ipv6Address != null) {
            builder.addAddress(params.ipv6Address, params.ipv6PrefixLen)
            builder.addRoute("::", 0)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            builder.setMetered(false)
        }
        if (Settings.allowBypass) {
            builder.allowBypass()
        }
        // Keep our own traffic (management calls, update checks) out of the tunnel.
        runCatching { builder.addDisallowedApplication(packageName) }
        val pfd = builder.establish() ?: throw IllegalStateException("establish() returned null")
        tun = pfd
        tunParams = params
        return pfd
    }

    /** Blocking native call; runs on [engine] only. */
    private fun runEngine(fd: Int, key: AivpnKey) {
        val result = try {
            AivpnJni.runTunnel(
                /* vpnService = */ this,
                /* tunFd = */ fd,
                /* serverHost = */ key.serverHost,
                /* serverPort = */ key.serverPort,
                /* serverKey = */ key.serverKey,
                /* psk = */ key.psk,
                /* mtlsCert = */ null,
                /* adaptiveLevel = */ ADAPTIVE_LEVEL,
                /* staticPrivkey = */ null,
                /* maskProfile = */ null,
                /* serverSigningKey = */ key.serverSigningKey,
                /* maskOperatorPubkey = */ key.maskOperatorPubkey,
                /* maskVerifyMode = */ if (key.serverSigningKey != null) MASK_VERIFY_WARN else MASK_VERIFY_OFF,
                /* polymorphicBase = */ null,
                /* shareMaskFeedback = */ false,
                /* receiveMaskHints = */ false,
                /* countryCode = */ null,
                /* priorOutcomesJson = */ null,
                /* cachedDescriptorsJson = */ null,
                /* transportName = */ null,
                /* transportParamsJson = */ null,
            )
        } catch (t: Throwable) { // never let a native failure kill the process silently
            Log.e(TAG, "runTunnel failed", t)
            t.message ?: t.javaClass.simpleName
        }
        finishSession(result)
    }

    /** Engine thread: the session ended on its own or because of a stop. */
    private fun finishSession(result: String) {
        runCatching { AivpnJni.clearPendingStop() }
        synchronized(tunLock) { closeTunLocked() }
        engine = null
        active = false
        finished = true
        // A user-initiated Disconnect() already flipped the UI to Stopped; a native error raised
        // while the session was being torn down must not resurrect a "Failed" banner after it.
        val userStopped = AivpnTunnel.status.value is AivpnStatus.Stopped
        if (closing || result.isEmpty() || userStopped) {
            AivpnTunnel.publish(AivpnStatus.Stopped)
        } else {
            Log.w(TAG, "tunnel stopped: $result")
            AivpnTunnel.publish(AivpnStatus.Failed(result))
        }
        stopForegroundCompat()
        stopSelf()
    }

    /** Stops the session and tears the service down; safe to call more than once. */
    private fun shutdown() {
        closing = true
        active = false
        if (AivpnJni.isAvailable) {
            runCatching { AivpnJni.stopTunnel() }
        }
        val worker = engine
        if (worker != null && worker !== Thread.currentThread()) {
            // Closing the tun fd under a live epoll loop would abort in native code.
            runCatching { worker.join(STOP_JOIN_MS) }
        }
        synchronized(tunLock) { closeTunLocked() }
        engine = null
        // A failure that already reached the UI must not be replaced by a plain "disconnected".
        if (!finished) AivpnTunnel.publish(AivpnStatus.Stopped)
        finished = true
        stopForegroundCompat()
        stopSelf()
    }

    /** Caller holds [tunLock]. */
    private fun closeTunLocked() {
        val fd = tun ?: return
        tun = null
        tunParams = null
        runCatching { fd.close() }
    }

    private fun fail(message: String) {
        Log.e(TAG, message)
        closing = true
        active = false
        finished = true
        AivpnTunnel.publish(AivpnStatus.Failed(message))
        synchronized(tunLock) { closeTunLocked() }
        engine = null
        stopForegroundCompat()
        stopSelf()
    }

    private fun createChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(NotificationManager::class.java) ?: return
        if (manager.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.aivpn_notification_channel),
            NotificationManager.IMPORTANCE_LOW,
        )
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(text: String): Notification {
        val openApp = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, AivpnVpnService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_fox)
            .setContentTitle(getString(R.string.aivpn_settings))
            .setContentText(text)
            .setContentIntent(openApp)
            .addAction(0, getString(R.string.stop), stopIntent)
            .setOngoing(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .build()
    }

    private fun startForegroundCompat(text: String): Boolean {
        createChannel()
        return runCatching {
            startForeground(NOTIFICATION_ID, buildNotification(text))
            true
        }.getOrElse {
            Log.e(TAG, "startForeground failed", it)
            false
        }
    }

    private fun updateNotification(text: String) {
        runCatching {
            val manager = getSystemService(NotificationManager::class.java) ?: return
            manager.notify(NOTIFICATION_ID, buildNotification(text))
        }
    }

    private fun stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            runCatching { stopForeground(Service.STOP_FOREGROUND_REMOVE) }
        } else {
            @Suppress("DEPRECATION")
            runCatching { stopForeground(true) }
        }
    }
}

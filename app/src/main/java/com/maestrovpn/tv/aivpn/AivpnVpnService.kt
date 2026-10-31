package com.maestrovpn.tv.aivpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
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
import kotlin.concurrent.thread

/**
 * VpnService that carries the experimental AIVPN protocol.
 *
 * It is deliberately independent from [com.maestrovpn.tv.bg.VPNService]: that one owns the
 * sing-box engine and is left untouched. Android permits one VPN interface per device, so both
 * sides stop the other before asking the system for theirs (see [AivpnTunnel.stopIfActive] and
 * [BoxService.stop] below).
 */
class AivpnVpnService : VpnService() {
    companion object {
        private const val TAG = "AivpnVpnService"
        private const val CHANNEL_ID = "aivpn"
        private const val NOTIFICATION_ID = 2
        private const val ACTION_STOP = "com.maestrovpn.tv.aivpn.STOP"
        private const val STOP_JOIN_MS = 2000L

        /** mask verification: 0 = off, 1 = warn, 2 = enforce (aivpn-core contract). */
        private const val MASK_VERIFY_OFF = 0
        private const val MASK_VERIFY_WARN = 1

        /** 0 = adaptive off; aivpn-core clamps the value into 0..3. */
        private const val ADAPTIVE_LEVEL = 0
    }

    @Volatile private var engine: Thread? = null
    @Volatile private var tun: ParcelFileDescriptor? = null

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
        if (engine != null) {
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
        if (prepare(this) != null) {
            fail(getString(R.string.aivpn_error_permission))
            return
        }

        // One VPN interface per device: stop the sing-box engine before asking for ours.
        BoxService.stop()

        val pfd = try {
            buildTun(key)
        } catch (e: IllegalArgumentException) { // e.g. "Cannot set address"
            fail(getString(R.string.aivpn_error_establish, e.message.orEmpty()))
            return
        } catch (e: IllegalStateException) {
            fail(getString(R.string.aivpn_error_establish, e.message.orEmpty()))
            return
        }
        if (pfd == null) {
            fail(getString(R.string.aivpn_error_establish, "establish() returned null"))
            return
        }
        tun = pfd

        // A pending-stop flag survives a session; without clearing it the next runTunnel would
        // return immediately (same trap the reference client documents).
        runCatching { AivpnJni.clearPendingStop() }

        val fd = pfd.fd
        engine = thread(name = "aivpn-tunnel") { runEngine(fd, key) }
        AivpnTunnel.publish(AivpnStatus.Running)
        updateNotification(getString(R.string.aivpn_notification_running))
    }

    private fun buildTun(key: AivpnKey): ParcelFileDescriptor? {
        val builder = Builder()
            .setSession("MaestroVPN AIVPN")
            .addAddress(key.vpnIp, key.prefixLen)
            .setMtu(key.mtu)
            // The Rust core drives the tun fd with epoll/AsyncFd; a blocking fd would stall it.
            .setBlocking(false)
            .addRoute("0.0.0.0", 0)
            .addDnsServer("1.1.1.1")
            .addDnsServer("8.8.8.8")
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            builder.setMetered(false)
        }
        if (Settings.allowBypass) {
            builder.allowBypass()
        }
        // Keep our own traffic (management calls, update checks) out of the tunnel.
        runCatching { builder.addDisallowedApplication(packageName) }
        return builder.establish()
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
        closeTun()
        engine = null
        if (result.isEmpty()) {
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
        if (AivpnJni.isAvailable) {
            runCatching { AivpnJni.stopTunnel() }
        }
        val worker = engine
        if (worker != null && worker !== Thread.currentThread()) {
            // Closing the tun fd under a live epoll loop would abort in native code.
            runCatching { worker.join(STOP_JOIN_MS) }
        }
        closeTun()
        engine = null
        AivpnTunnel.publish(AivpnStatus.Stopped)
        stopForegroundCompat()
        stopSelf()
    }

    @Synchronized
    private fun closeTun() {
        val fd = tun ?: return
        tun = null
        runCatching { fd.close() }
    }

    private fun fail(message: String) {
        Log.e(TAG, message)
        AivpnTunnel.publish(AivpnStatus.Failed(message))
        closeTun()
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
            runCatching { stopForeground(STOP_FOREGROUND_REMOVE) }
        } else {
            @Suppress("DEPRECATION")
            runCatching { stopForeground(true) }
        }
    }
}

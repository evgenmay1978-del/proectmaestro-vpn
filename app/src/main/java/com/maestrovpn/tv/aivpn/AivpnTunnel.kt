package com.maestrovpn.tv.aivpn

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import com.aivpn.client.AivpnJni
import com.maestrovpn.tv.R
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Observable lifecycle of the experimental AIVPN tunnel. */
sealed interface AivpnStatus {
    data object Stopped : AivpnStatus
    data object Connecting : AivpnStatus
    data object Running : AivpnStatus
    data class Failed(val message: String) : AivpnStatus
}

/**
 * Process-wide handle on the AIVPN tunnel: the single place the UI reads the status from and
 * starts/stops [AivpnVpnService].
 *
 * Android owns exactly one VPN interface at a time, so this object is also the seam that keeps
 * AIVPN and the sing-box engine from fighting over it (see [stopIfActive]).
 */
object AivpnTunnel {
    private val _status = MutableStateFlow<AivpnStatus>(AivpnStatus.Stopped)
    val status: StateFlow<AivpnStatus> = _status.asStateFlow()

    val isActive: Boolean
        get() = _status.value is AivpnStatus.Connecting || _status.value is AivpnStatus.Running

    /** Called by [AivpnVpnService]; the UI only reads [status]. */
    internal fun publish(status: AivpnStatus) {
        _status.value = status
    }

    fun start(context: Context) {
        val app = context.applicationContext
        if (!AivpnJni.isAvailable) {
            _status.value = AivpnStatus.Failed(
                app.getString(R.string.aivpn_error_library_missing, AivpnJni.loadError.orEmpty()),
            )
            return
        }
        if (isActive) return
        _status.value = AivpnStatus.Connecting
        try {
            ContextCompat.startForegroundService(app, Intent(app, AivpnVpnService::class.java))
        } catch (e: RuntimeException) { // e.g. background FGS start restriction
            _status.value = AivpnStatus.Failed(e.message ?: app.getString(R.string.aivpn_error_start_failed))
        }
    }

    fun stop(context: Context) {
        val app = context.applicationContext
        // A live VpnService stays bound to the system VPN framework: stopService() marks it
        // stopped without running onDestroy() while the tun is up, and a start request can be
        // swallowed entirely - that is how the interface (and the system key) survived the
        // "disconnected" UI. Talk to the live instance instead; ACTION_STOP stays the fallback
        // for a cold process that no longer holds one.
        if (AivpnVpnService.current != null) {
            AivpnVpnService.requestStop()
            return
        }
        val service = Intent(app, AivpnVpnService::class.java)
        runCatching { app.startService(Intent(service).setAction(AivpnVpnService.ACTION_STOP)) }
        if (AivpnJni.isAvailable) runCatching { AivpnJni.stopTunnel() }
        // No live service: nothing owns the tunnel, so the UI can flip right away.
        if (_status.value is AivpnStatus.Connecting || _status.value is AivpnStatus.Running) {
            _status.value = AivpnStatus.Stopped
        }
    }

    /**
     * Drops the AIVPN tunnel if it is up. Used right before the sing-box engine starts: letting
     * both run would only make the system revoke one of them in the middle of a session.
     */
    fun stopIfActive(context: Context) {
        if (!isActive) return
        stop(context)
        if (AivpnJni.isAvailable) runCatching { AivpnJni.stopTunnel() }
    }
}

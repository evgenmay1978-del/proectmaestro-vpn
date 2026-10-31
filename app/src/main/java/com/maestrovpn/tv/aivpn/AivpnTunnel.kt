package com.maestrovpn.tv.aivpn

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import com.aivpn.client.AivpnJni
import com.maestrovpn.tv.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

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
    private const val REAPPLY_STOP_TIMEOUT_MS = 4000L

    private val _status = MutableStateFlow<AivpnStatus>(AivpnStatus.Stopped)
    val status: StateFlow<AivpnStatus> = _status.asStateFlow()

    private val scope = CoroutineScope(Dispatchers.Main.immediate + SupervisorJob())

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
     * Rebuilds a live tunnel so a changed per-app selection takes effect now.
     *
     * The package list is fixed at VpnService.Builder.establish() time, so an already established
     * interface keeps carrying the apps it was built with. sing-box solves this with
     * serviceReload (see the ApplyServiceChange handler in MainActivity); AIVPN has no reload
     * command, so the honest equivalent is stop -> wait for the interface to be released -> start.
     *
     * No-op when the tunnel is down: the next start picks the new selection up anyway.
     */
    fun reapplyPackagePolicy(context: Context) {
        if (!isActive) return
        val app = context.applicationContext
        scope.launch {
            stop(app)
            // start() refuses to run while the old session still owns the interface.
            withTimeoutOrNull(REAPPLY_STOP_TIMEOUT_MS) {
                status.first { it is AivpnStatus.Stopped || it is AivpnStatus.Failed }
            }
            start(app)
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

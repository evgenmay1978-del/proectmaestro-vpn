package com.maestrovpn.tv.whitelist

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.random.Random

/**
 * Runs [WhiteListWatchdog] against the real world (audit 28.10.2026, D3).
 *
 * The state machine is pure and fully unit-tested; this class owns the impure parts: timers, the
 * probe itself and the side effects the machine asks for. Everything here is deliberately
 * synchronous + coroutine-free at the boundary: [dispatch] is safe to call from any thread (service
 * callbacks, network callbacks), state lives behind one lock, and each action runs outside it.
 *
 * Callbacks:
 *  - [onLive] the first successful probe: the tunnel is PROVEN to carry traffic. The service moves
 *    its status to Started only here, never before.
 *  - [onRedial] re-apply the configuration (cheap local redial, no user interaction).
 *  - [onFallbackOrdinary] this path is dead: leave the CDN edge for the ordinary nodes, or give up
 *    honestly when there is nothing to fall back to — the caller decides, the machine only reports.
 *  - [onClearSession] drop the current CDN/edge session.
 */
internal class TunnelWatchdog(
    private val probe: suspend (timeoutMillis: Long) -> Boolean,
    private val onLive: () -> Unit,
    private val onRedial: (edgeIndex: Int) -> Unit,
    private val onFallbackOrdinary: () -> Unit,
    private val onClearSession: () -> Unit = {},
    private val log: (String) -> Unit = {},
    private val policy: WhiteListWatchdogPolicy = POLICY,
    private val random: Random = Random.Default,
    private val startupJitterMax: Long = 2_000,
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
) {
    companion object {
        /**
         * Startup must converge in ~30 s: the first failure redials at once (the usual cause is a
         * stale edge or a race with the tun coming up), the third one abandons this path. Steady
         * state probes every 25-35 s — the same cadence the state machine ships with.
         */
        val POLICY = WhiteListWatchdogPolicy(
            intervalMinMillis = 25_000,
            intervalMaxMillis = 35_000,
            probeTimeoutMillis = 9_000,
            baseBackoffMillis = 3_000,
            maxBackoffMillis = 12_000,
            redialAfterFailures = 1,
            advanceEdgeAfterFailures = 3,
        )
    }

    private val lock = Any()
    private val machine = WhiteListWatchdog(policy)
    private var snapshot = WatchdogSnapshot.stopped()
    private var timerJob: Job? = null
    private var probeJob: Job? = null
    private var networkEpoch = -1L
    private var live = false
    private var stopped = false
    private val closed = AtomicBoolean(false)

    internal val isLive: Boolean get() = synchronized(lock) { live }

    fun start(edgeCount: Int) = dispatch(WatchdogEvent.Start(maxOf(1, edgeCount)))

    fun networkAvailable() = dispatch(WatchdogEvent.NetworkAvailable(synchronized(lock) { ++networkEpoch }))

    fun networkLost() {
        val ticket = synchronized(lock) { snapshot.networkTicket } ?: return
        dispatch(WatchdogEvent.NetworkLost(ticket))
    }

    fun wake() = dispatch(WatchdogEvent.Wake)

    fun stop() {
        close()
        synchronized(lock) {
            timerJob?.cancel()
            probeJob?.cancel()
            timerJob = null
            probeJob = null
            stopped = true
            snapshot = WatchdogSnapshot.stopped(
                lastNetworkEpoch = snapshot.lastNetworkEpoch,
                generation = snapshot.generation,
            )
        }
        scope.cancel()
    }

    /**
     * Detached: callbacks run from service/network threads and must not wait for the watchdog (a
     * redial re-applies the config and can block for seconds).
     */
    fun dispatchDetached(event: WatchdogEvent) {
        if (closed.get()) return
        scope.launch { dispatch(event) }
    }

    private fun close() {
        closed.set(true)
    }

    private fun dispatch(event: WatchdogEvent) {
        if (closed.get()) return
        var actions: List<WatchdogAction> = emptyList()
        var becameLive = false
        var state = WatchdogState.STOPPED
        synchronized(lock) {
            if (closed.get() || stopped) return
            val jitter = when (event) {
                is WatchdogEvent.ProbeSucceeded -> {
                    val min = policy.intervalMinMillis
                    val max = policy.intervalMaxMillis
                    min + random.nextLong(max - min + 1)
                }
                is WatchdogEvent.NetworkAvailable, WatchdogEvent.Wake ->
                    random.nextLong(startupJitterMax + 1)
                else -> 0L
            }
            val reduced = try {
                machine.reduce(snapshot, event, jitter)
            } catch (e: IllegalArgumentException) {
                // Tickets from a superseded network/generation are not errors: the machine rejects
                // what it did not ask for, and the live schedule continues unchanged.
                log("watchdog ignored " + event + ": " + e.message)
                return
            }
            snapshot = reduced.snapshot
            state = reduced.snapshot.state
            actions = reduced.actions
            if (event is WatchdogEvent.ProbeSucceeded && !live) {
                live = true
                becameLive = true
            }
        }
        if (actions.isNotEmpty()) log("watchdog " + state + " " + actions.joinToString(",") { it.javaClass.simpleName })
        for (action in actions) {
            runCatching { execute(action) }.onFailure { log("watchdog action failed: " + it.message) }
        }
        if (becameLive) {
            log("watchdog tunnel is live")
            runCatching { onLive() }.onFailure { log("onLive failed: " + it.message) }
        }
    }

    private fun execute(action: WatchdogAction) {
        when (action) {
            is WatchdogAction.Schedule -> {
                timerJob?.cancel()
                timerJob = scope.launch {
                    delay(action.delayMillis)
                    dispatch(WatchdogEvent.Timer(action.ticket))
                }
            }
            is WatchdogAction.Probe -> {
                probeJob?.cancel()
                probeJob = scope.launch {
                    val ok = runCatching { probe(action.timeoutMillis) }
                        .onFailure { log("probe threw: " + it.message) }
                        .getOrDefault(false)
                    dispatch(
                        if (ok) WatchdogEvent.ProbeSucceeded(action.ticket) else WatchdogEvent.ProbeFailed(action.ticket),
                    )
                }
            }
            is WatchdogAction.ControlledRedial -> {
                log("watchdog redial edge=" + action.edgeIndex)
                onRedial(action.edgeIndex)
            }
            WatchdogAction.CancelProbe -> {
                probeJob?.cancel()
                probeJob = null
            }
            WatchdogAction.ClearSession -> onClearSession()
            WatchdogAction.FallbackOrdinaryVpn -> onFallbackOrdinary()
        }
    }
}

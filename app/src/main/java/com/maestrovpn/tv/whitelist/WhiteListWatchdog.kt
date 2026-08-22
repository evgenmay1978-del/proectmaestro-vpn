package com.maestrovpn.tv.whitelist

data class WhiteListWatchdogPolicy(
    val intervalMinMillis: Long = 25_000,
    val intervalMaxMillis: Long = 35_000,
    val probeTimeoutMillis: Long = 9_000,
    val baseBackoffMillis: Long = 5_000,
    val maxBackoffMillis: Long = 120_000,
    val redialAfterFailures: Int = 3,
    val advanceEdgeAfterFailures: Int = 6,
)

enum class WatchdogState {
    STOPPED,
    WAITING_NETWORK,
    SCHEDULED,
    PROBING,
    BACKING_OFF,
    FALLBACK,
}

data class WatchdogSnapshot(
    val state: WatchdogState,
    val edgeIndex: Int,
    val edgeCount: Int,
    val failures: Int,
    val networkEpoch: Long?,
) {
    companion object {
        fun stopped() = WatchdogSnapshot(
            state = WatchdogState.STOPPED,
            edgeIndex = 0,
            edgeCount = 0,
            failures = 0,
            networkEpoch = null,
        )
    }
}

sealed interface WatchdogEvent {
    data class Start(val edgeCount: Int) : WatchdogEvent

    data class NetworkAvailable(val epoch: Long) : WatchdogEvent

    data object NetworkLost : WatchdogEvent

    data object Timer : WatchdogEvent

    data object ProbeSucceeded : WatchdogEvent

    data object ProbeFailed : WatchdogEvent

    data object Wake : WatchdogEvent

    data object Stop : WatchdogEvent
}

sealed interface WatchdogAction {
    data class Schedule(val delayMillis: Long) : WatchdogAction

    data class Probe(val timeoutMillis: Long) : WatchdogAction

    data class ControlledRedial(val edgeIndex: Int) : WatchdogAction

    data object CancelProbe : WatchdogAction

    data object ClearSession : WatchdogAction

    data object FallbackOrdinaryVpn : WatchdogAction
}

data class WhiteListWatchdogResult(
    val snapshot: WatchdogSnapshot,
    val actions: List<WatchdogAction>,
)

class WhiteListWatchdog(
    private val policy: WhiteListWatchdogPolicy = WhiteListWatchdogPolicy(),
) {
    init {
        require(policy.intervalMinMillis > 0)
        require(policy.intervalMaxMillis >= policy.intervalMinMillis)
        require(policy.probeTimeoutMillis > 0)
        require(policy.baseBackoffMillis > 0)
        require(policy.maxBackoffMillis >= policy.baseBackoffMillis)
        require(policy.redialAfterFailures > 0)
        require(policy.advanceEdgeAfterFailures > policy.redialAfterFailures)
    }

    fun reduce(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent,
        jitterMillis: Long = 0,
    ): WhiteListWatchdogResult {
        validateSnapshot(snapshot)
        return when (event) {
            is WatchdogEvent.Start -> start(snapshot, event, jitterMillis)
            is WatchdogEvent.NetworkAvailable -> networkAvailable(snapshot, event, jitterMillis)
            WatchdogEvent.NetworkLost -> networkLost(snapshot, jitterMillis)
            WatchdogEvent.Timer -> timer(snapshot, jitterMillis)
            WatchdogEvent.ProbeSucceeded -> probeSucceeded(snapshot, jitterMillis)
            WatchdogEvent.ProbeFailed -> probeFailed(snapshot, jitterMillis)
            WatchdogEvent.Wake -> wake(snapshot, jitterMillis)
            WatchdogEvent.Stop -> stop(jitterMillis)
        }
    }

    private fun start(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.Start,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        require(snapshot == WatchdogSnapshot.stopped())
        if (event.edgeCount < 1) {
            return WhiteListWatchdogResult(
                snapshot = WatchdogSnapshot(
                    state = WatchdogState.FALLBACK,
                    edgeIndex = 0,
                    edgeCount = 0,
                    failures = 0,
                    networkEpoch = null,
                ),
                actions = listOf(WatchdogAction.FallbackOrdinaryVpn),
            )
        }
        return WhiteListWatchdogResult(
            snapshot = WatchdogSnapshot(
                state = WatchdogState.WAITING_NETWORK,
                edgeIndex = 0,
                edgeCount = event.edgeCount,
                failures = 0,
                networkEpoch = null,
            ),
            actions = emptyList(),
        )
    }

    private fun networkAvailable(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.NetworkAvailable,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(snapshot.state !in setOf(WatchdogState.STOPPED, WatchdogState.FALLBACK))
        require(event.epoch >= 0)
        if (snapshot.networkEpoch == event.epoch) {
            return WhiteListWatchdogResult(snapshot, emptyList())
        }

        val schedule = WatchdogAction.Schedule(startupJitter(jitterMillis))
        val next = snapshot.copy(
            state = WatchdogState.SCHEDULED,
            networkEpoch = event.epoch,
        )
        if (snapshot.networkEpoch == null) {
            return WhiteListWatchdogResult(next, listOf(schedule))
        }
        return WhiteListWatchdogResult(
            snapshot = next,
            actions = listOf(
                WatchdogAction.CancelProbe,
                WatchdogAction.ClearSession,
                WatchdogAction.ControlledRedial(snapshot.edgeIndex),
                schedule,
            ),
        )
    }

    private fun networkLost(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        require(snapshot.state in ONLINE_STATES)
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.WAITING_NETWORK,
                networkEpoch = null,
            ),
            actions = listOf(WatchdogAction.CancelProbe),
        )
    }

    private fun timer(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        require(snapshot.state == WatchdogState.SCHEDULED || snapshot.state == WatchdogState.BACKING_OFF)
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(state = WatchdogState.PROBING),
            actions = listOf(WatchdogAction.Probe(policy.probeTimeoutMillis)),
        )
    }

    private fun probeSucceeded(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(snapshot.state == WatchdogState.PROBING)
        require(jitterMillis in policy.intervalMinMillis..policy.intervalMaxMillis)
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.SCHEDULED,
                failures = 0,
            ),
            actions = listOf(WatchdogAction.Schedule(jitterMillis)),
        )
    }

    private fun probeFailed(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        require(snapshot.state == WatchdogState.PROBING)
        val failureCount = snapshot.failures + 1
        if (failureCount == policy.advanceEdgeAfterFailures) {
            if (snapshot.edgeIndex == snapshot.edgeCount - 1) {
                return WhiteListWatchdogResult(
                    snapshot = snapshot.copy(
                        state = WatchdogState.FALLBACK,
                        failures = failureCount,
                        networkEpoch = null,
                    ),
                    actions = listOf(
                        WatchdogAction.CancelProbe,
                        WatchdogAction.ClearSession,
                        WatchdogAction.FallbackOrdinaryVpn,
                    ),
                )
            }
            val nextEdge = snapshot.edgeIndex + 1
            return WhiteListWatchdogResult(
                snapshot = snapshot.copy(
                    state = WatchdogState.BACKING_OFF,
                    edgeIndex = nextEdge,
                    failures = 0,
                ),
                actions = listOf(
                    WatchdogAction.ClearSession,
                    WatchdogAction.ControlledRedial(nextEdge),
                    WatchdogAction.Schedule(backoffMillis(failureCount)),
                ),
            )
        }

        val schedule = WatchdogAction.Schedule(backoffMillis(failureCount))
        val actions = if (failureCount == policy.redialAfterFailures) {
            listOf(
                WatchdogAction.ClearSession,
                WatchdogAction.ControlledRedial(snapshot.edgeIndex),
                schedule,
            )
        } else {
            listOf(schedule)
        }
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.BACKING_OFF,
                failures = failureCount,
            ),
            actions = actions,
        )
    }

    private fun wake(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(snapshot.state !in setOf(WatchdogState.STOPPED, WatchdogState.FALLBACK))
        if (snapshot.networkEpoch == null) {
            return WhiteListWatchdogResult(snapshot, emptyList())
        }
        val actions = buildList {
            if (snapshot.state == WatchdogState.PROBING) add(WatchdogAction.CancelProbe)
            add(WatchdogAction.Schedule(startupJitter(jitterMillis)))
        }
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(state = WatchdogState.SCHEDULED),
            actions = actions,
        )
    }

    private fun stop(jitterMillis: Long): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        return WhiteListWatchdogResult(
            snapshot = WatchdogSnapshot.stopped(),
            actions = listOf(WatchdogAction.CancelProbe, WatchdogAction.ClearSession),
        )
    }

    private fun startupJitter(jitterMillis: Long): Long = jitterMillis.coerceIn(0L, 2_000L)

    private fun backoffMillis(failureCount: Int): Long {
        var delay = policy.baseBackoffMillis
        repeat(failureCount - 1) {
            delay = if (delay >= policy.maxBackoffMillis) {
                policy.maxBackoffMillis
            } else if (delay > policy.maxBackoffMillis / 2) {
                policy.maxBackoffMillis
            } else {
                delay * 2
            }
        }
        return delay.coerceAtMost(policy.maxBackoffMillis)
    }

    private fun validateSnapshot(snapshot: WatchdogSnapshot) {
        require(snapshot.edgeCount >= 0)
        require(snapshot.edgeIndex >= 0)
        require(snapshot.failures >= 0)
        require(snapshot.networkEpoch == null || snapshot.networkEpoch >= 0)
        if (snapshot.edgeCount == 0) {
            require(snapshot.edgeIndex == 0)
        } else {
            require(snapshot.edgeIndex < snapshot.edgeCount)
        }
        when (snapshot.state) {
            WatchdogState.STOPPED -> require(snapshot == WatchdogSnapshot.stopped())
            WatchdogState.WAITING_NETWORK -> {
                require(snapshot.edgeCount > 0)
                require(snapshot.networkEpoch == null)
                require(snapshot.failures < policy.advanceEdgeAfterFailures)
            }
            WatchdogState.SCHEDULED,
            WatchdogState.PROBING,
            WatchdogState.BACKING_OFF,
            -> {
                require(snapshot.edgeCount > 0)
                require(snapshot.networkEpoch != null)
                require(snapshot.failures < policy.advanceEdgeAfterFailures)
            }
            WatchdogState.FALLBACK -> require(snapshot.networkEpoch == null)
        }
    }

    private companion object {
        val ONLINE_STATES = setOf(
            WatchdogState.SCHEDULED,
            WatchdogState.PROBING,
            WatchdogState.BACKING_OFF,
        )
    }
}

enum class WhiteListLogCode {
    STARTED,
    NETWORK_AVAILABLE,
    NETWORK_LOST,
    PROBE_STARTED,
    PROBE_SUCCEEDED,
    PROBE_FAILED,
    REDIAL,
    EDGE_ADVANCED,
    FALLBACK,
    STOPPED,
}

data class WhiteListLogEvent(
    val code: WhiteListLogCode,
    val state: WatchdogState,
    val failureCount: Int,
    val edgeOrdinal: Int,
) {
    init {
        require(failureCount >= 0)
        require(edgeOrdinal >= 0)
    }

    fun renderSafe(): String =
        "code=${code.name} state=${state.name} failures=$failureCount edge=$edgeOrdinal"
}

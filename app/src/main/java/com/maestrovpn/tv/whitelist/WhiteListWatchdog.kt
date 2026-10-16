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

data class WatchdogNetworkTicket(
    val epoch: Long,
    val generation: Long,
) {
    init {
        require(epoch >= 0)
        require(generation > 0)
    }
}

data class WatchdogScheduleTicket(
    val network: WatchdogNetworkTicket,
    val generation: Long,
) {
    init {
        require(generation > 0)
    }
}

data class WatchdogProbeTicket(
    val network: WatchdogNetworkTicket,
    val generation: Long,
) {
    init {
        require(generation > 0)
    }
}

data class WatchdogSnapshot(
    val state: WatchdogState,
    val edgeIndex: Int,
    val edgeCount: Int,
    val failures: Int,
    val networkTicket: WatchdogNetworkTicket?,
    val scheduleTicket: WatchdogScheduleTicket?,
    val probeTicket: WatchdogProbeTicket?,
    val lastNetworkEpoch: Long?,
    val generation: Long,
) {
    companion object {
        fun stopped(
            lastNetworkEpoch: Long? = null,
            generation: Long = 0,
        ) = WatchdogSnapshot(
            state = WatchdogState.STOPPED,
            edgeIndex = 0,
            edgeCount = 0,
            failures = 0,
            networkTicket = null,
            scheduleTicket = null,
            probeTicket = null,
            lastNetworkEpoch = lastNetworkEpoch,
            generation = generation,
        )
    }
}

sealed interface WatchdogEvent {
    data class Start(val edgeCount: Int) : WatchdogEvent

    data class NetworkAvailable(val epoch: Long) : WatchdogEvent

    data class NetworkLost(val ticket: WatchdogNetworkTicket) : WatchdogEvent

    data class Timer(val ticket: WatchdogScheduleTicket) : WatchdogEvent

    data class ProbeSucceeded(val ticket: WatchdogProbeTicket) : WatchdogEvent

    data class ProbeFailed(val ticket: WatchdogProbeTicket) : WatchdogEvent

    data object Wake : WatchdogEvent

    data object Stop : WatchdogEvent
}

sealed interface WatchdogAction {
    data class Schedule(
        val delayMillis: Long,
        val ticket: WatchdogScheduleTicket,
    ) : WatchdogAction

    data class Probe(
        val timeoutMillis: Long,
        val ticket: WatchdogProbeTicket,
    ) : WatchdogAction

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
            is WatchdogEvent.NetworkLost -> networkLost(snapshot, event, jitterMillis)
            is WatchdogEvent.Timer -> timer(snapshot, event, jitterMillis)
            is WatchdogEvent.ProbeSucceeded -> probeSucceeded(snapshot, event, jitterMillis)
            is WatchdogEvent.ProbeFailed -> probeFailed(snapshot, event, jitterMillis)
            WatchdogEvent.Wake -> wake(snapshot, jitterMillis)
            WatchdogEvent.Stop -> stop(snapshot, jitterMillis)
        }
    }

    private fun start(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.Start,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        require(snapshot.state == WatchdogState.STOPPED)
        if (event.edgeCount < 1) {
            return WhiteListWatchdogResult(
                snapshot = snapshot.copy(state = WatchdogState.FALLBACK),
                actions = listOf(WatchdogAction.FallbackOrdinaryVpn),
            )
        }
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.WAITING_NETWORK,
                edgeCount = event.edgeCount,
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
        if (snapshot.networkTicket?.epoch == event.epoch) {
            return noOp(snapshot)
        }
        if (snapshot.lastNetworkEpoch?.let { event.epoch <= it } == true) {
            return noOp(snapshot)
        }

        val networkGeneration = nextGeneration(snapshot.generation)
        val network = WatchdogNetworkTicket(event.epoch, networkGeneration)
        val scheduled = armSchedule(
            snapshot = snapshot.copy(
                networkTicket = network,
                scheduleTicket = null,
                probeTicket = null,
                lastNetworkEpoch = event.epoch,
                generation = networkGeneration,
            ),
            state = WatchdogState.SCHEDULED,
            delayMillis = startupJitter(jitterMillis),
        )
        if (snapshot.networkTicket == null) {
            return scheduled
        }
        return scheduled.copy(
            actions = listOf(
                WatchdogAction.CancelProbe,
                WatchdogAction.ClearSession,
                WatchdogAction.ControlledRedial(snapshot.edgeIndex),
                scheduled.actions.single(),
            ),
        )
    }

    private fun networkLost(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.NetworkLost,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        if (event.ticket != snapshot.networkTicket) {
            return noOp(snapshot)
        }
        require(snapshot.state in ONLINE_STATES)
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.WAITING_NETWORK,
                networkTicket = null,
                scheduleTicket = null,
                probeTicket = null,
            ),
            actions = listOf(WatchdogAction.CancelProbe),
        )
    }

    private fun timer(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.Timer,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        if (event.ticket != snapshot.scheduleTicket || event.ticket.network != snapshot.networkTicket) {
            return noOp(snapshot)
        }
        require(snapshot.state == WatchdogState.SCHEDULED || snapshot.state == WatchdogState.BACKING_OFF)
        val probeGeneration = nextGeneration(snapshot.generation)
        val ticket = WatchdogProbeTicket(
            network = requireNotNull(snapshot.networkTicket),
            generation = probeGeneration,
        )
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = WatchdogState.PROBING,
                scheduleTicket = null,
                probeTicket = ticket,
                generation = probeGeneration,
            ),
            actions = listOf(WatchdogAction.Probe(policy.probeTimeoutMillis, ticket)),
        )
    }

    private fun probeSucceeded(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.ProbeSucceeded,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        if (event.ticket != snapshot.probeTicket || event.ticket.network != snapshot.networkTicket) {
            return noOp(snapshot)
        }
        require(snapshot.state == WatchdogState.PROBING)
        require(jitterMillis in policy.intervalMinMillis..policy.intervalMaxMillis)
        return armSchedule(
            snapshot = snapshot.copy(
                failures = 0,
                probeTicket = null,
            ),
            state = WatchdogState.SCHEDULED,
            delayMillis = jitterMillis,
        )
    }

    private fun probeFailed(
        snapshot: WatchdogSnapshot,
        event: WatchdogEvent.ProbeFailed,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        if (event.ticket != snapshot.probeTicket || event.ticket.network != snapshot.networkTicket) {
            return noOp(snapshot)
        }
        require(jitterMillis == 0L)
        require(snapshot.state == WatchdogState.PROBING)
        val failureCount = snapshot.failures + 1
        if (failureCount == policy.advanceEdgeAfterFailures) {
            if (snapshot.edgeIndex == snapshot.edgeCount - 1) {
                return WhiteListWatchdogResult(
                    snapshot = snapshot.copy(
                        state = WatchdogState.FALLBACK,
                        failures = failureCount,
                        networkTicket = null,
                        scheduleTicket = null,
                        probeTicket = null,
                    ),
                    actions = listOf(
                        WatchdogAction.CancelProbe,
                        WatchdogAction.ClearSession,
                        WatchdogAction.FallbackOrdinaryVpn,
                    ),
                )
            }
            val nextEdge = snapshot.edgeIndex + 1
            val scheduled = armSchedule(
                snapshot = snapshot.copy(
                    edgeIndex = nextEdge,
                    failures = 0,
                    probeTicket = null,
                ),
                state = WatchdogState.BACKING_OFF,
                delayMillis = backoffMillis(failureCount),
            )
            return scheduled.copy(
                actions = listOf(
                    WatchdogAction.ClearSession,
                    WatchdogAction.ControlledRedial(nextEdge),
                    scheduled.actions.single(),
                ),
            )
        }

        val scheduled = armSchedule(
            snapshot = snapshot.copy(
                failures = failureCount,
                probeTicket = null,
            ),
            state = WatchdogState.BACKING_OFF,
            delayMillis = backoffMillis(failureCount),
        )
        if (failureCount != policy.redialAfterFailures) {
            return scheduled
        }
        return scheduled.copy(
            actions = listOf(
                WatchdogAction.ClearSession,
                WatchdogAction.ControlledRedial(snapshot.edgeIndex),
                scheduled.actions.single(),
            ),
        )
    }

    private fun wake(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(snapshot.state !in setOf(WatchdogState.STOPPED, WatchdogState.FALLBACK))
        val previousNetwork = snapshot.networkTicket ?: return noOp(snapshot)
        val networkGeneration = nextGeneration(snapshot.generation)
        val network = previousNetwork.copy(generation = networkGeneration)
        val scheduled = armSchedule(
            snapshot = snapshot.copy(
                networkTicket = network,
                scheduleTicket = null,
                probeTicket = null,
                generation = networkGeneration,
            ),
            state = WatchdogState.SCHEDULED,
            delayMillis = startupJitter(jitterMillis),
        )
        val actions = buildList {
            if (snapshot.state == WatchdogState.PROBING) add(WatchdogAction.CancelProbe)
            add(scheduled.actions.single())
        }
        return scheduled.copy(actions = actions)
    }

    private fun stop(
        snapshot: WatchdogSnapshot,
        jitterMillis: Long,
    ): WhiteListWatchdogResult {
        require(jitterMillis == 0L)
        if (snapshot.state == WatchdogState.STOPPED) {
            return noOp(snapshot)
        }
        return WhiteListWatchdogResult(
            snapshot = WatchdogSnapshot.stopped(
                lastNetworkEpoch = snapshot.lastNetworkEpoch,
                generation = snapshot.generation,
            ),
            actions = listOf(WatchdogAction.CancelProbe, WatchdogAction.ClearSession),
        )
    }

    private fun armSchedule(
        snapshot: WatchdogSnapshot,
        state: WatchdogState,
        delayMillis: Long,
    ): WhiteListWatchdogResult {
        require(state == WatchdogState.SCHEDULED || state == WatchdogState.BACKING_OFF)
        val generation = nextGeneration(snapshot.generation)
        val ticket = WatchdogScheduleTicket(
            network = requireNotNull(snapshot.networkTicket),
            generation = generation,
        )
        return WhiteListWatchdogResult(
            snapshot = snapshot.copy(
                state = state,
                scheduleTicket = ticket,
                probeTicket = null,
                generation = generation,
            ),
            actions = listOf(WatchdogAction.Schedule(delayMillis, ticket)),
        )
    }

    private fun nextGeneration(current: Long): Long {
        require(current < Long.MAX_VALUE)
        return current + 1
    }

    private fun noOp(snapshot: WatchdogSnapshot) = WhiteListWatchdogResult(snapshot, emptyList())

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
        require(snapshot.lastNetworkEpoch == null || snapshot.lastNetworkEpoch >= 0)
        require(snapshot.generation >= 0)
        snapshot.networkTicket?.let {
            require(it.generation <= snapshot.generation)
            require(it.epoch == snapshot.lastNetworkEpoch)
        }
        snapshot.scheduleTicket?.let {
            require(it.generation <= snapshot.generation)
            require(it.network == snapshot.networkTicket)
        }
        snapshot.probeTicket?.let {
            require(it.generation <= snapshot.generation)
            require(it.network == snapshot.networkTicket)
        }
        if (snapshot.edgeCount == 0) {
            require(snapshot.edgeIndex == 0)
        } else {
            require(snapshot.edgeIndex < snapshot.edgeCount)
        }
        when (snapshot.state) {
            WatchdogState.STOPPED -> {
                require(snapshot.edgeCount == 0)
                require(snapshot.failures == 0)
                require(snapshot.networkTicket == null)
                require(snapshot.scheduleTicket == null)
                require(snapshot.probeTicket == null)
            }
            WatchdogState.WAITING_NETWORK -> {
                require(snapshot.edgeCount > 0)
                require(snapshot.networkTicket == null)
                require(snapshot.scheduleTicket == null)
                require(snapshot.probeTicket == null)
                require(snapshot.failures < policy.advanceEdgeAfterFailures)
            }
            WatchdogState.SCHEDULED,
            WatchdogState.BACKING_OFF,
            -> {
                require(snapshot.edgeCount > 0)
                require(snapshot.networkTicket != null)
                require(snapshot.scheduleTicket != null)
                require(snapshot.probeTicket == null)
                require(snapshot.failures < policy.advanceEdgeAfterFailures)
            }
            WatchdogState.PROBING -> {
                require(snapshot.edgeCount > 0)
                require(snapshot.networkTicket != null)
                require(snapshot.scheduleTicket == null)
                require(snapshot.probeTicket != null)
                require(snapshot.failures < policy.advanceEdgeAfterFailures)
            }
            WatchdogState.FALLBACK -> {
                require(snapshot.networkTicket == null)
                require(snapshot.scheduleTicket == null)
                require(snapshot.probeTicket == null)
            }
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

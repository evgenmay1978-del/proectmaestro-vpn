package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class WhiteListWatchdogTest {
    private val watchdog = WhiteListWatchdog()

    @Test
    fun successSchedulesJitteredHeartbeat() {
        val online = onlineSnapshot()
        val probing = probing(online)
        val healthy = watchdog.reduce(
            probing,
            WatchdogEvent.ProbeSucceeded(requireNotNull(probing.probeTicket)),
            jitterMillis = 30_000,
        )

        assertEquals(
            listOf(scheduleAction(healthy.snapshot, 30_000)),
            healthy.actions,
        )
        assertEquals(0, healthy.snapshot.failures)
        assertEquals(WatchdogState.SCHEDULED, healthy.snapshot.state)
    }

    @Test
    fun replacementNetworkClearsAndRedialsOnce() {
        val snapshot = onlineSnapshot()
        val result = watchdog.reduce(
            snapshot,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 700,
        )

        assertEquals(
            listOf(
                WatchdogAction.CancelProbe,
                WatchdogAction.ClearSession,
                WatchdogAction.ControlledRedial(edgeIndex = 0),
                scheduleAction(result.snapshot, 700),
            ),
            result.actions,
        )

        val duplicate = watchdog.reduce(
            result.snapshot,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 900,
        )
        assertEquals(result.snapshot, duplicate.snapshot)
        assertTrue(duplicate.actions.isEmpty())
    }

    @Test
    fun networkLossWaitsWithoutRedial() {
        val probing = probing(onlineSnapshot())
        val result = watchdog.reduce(
            probing,
            WatchdogEvent.NetworkLost(requireNotNull(probing.networkTicket)),
        )

        assertEquals(WatchdogState.WAITING_NETWORK, result.snapshot.state)
        assertEquals(null, result.snapshot.networkTicket)
        assertEquals(listOf(WatchdogAction.CancelProbe), result.actions)

        val restored = watchdog.reduce(
            result.snapshot,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 400,
        )
        assertEquals(listOf(scheduleAction(restored.snapshot, 400)), restored.actions)
    }

    @Test
    fun backoffIsBoundedAndSixthFailureAdvancesEdgeInOrder() {
        var snapshot = onlineSnapshot(edgeCount = 2)
        val delays = mutableListOf<Long>()

        repeat(6) { zeroBasedFailure ->
            val probing = probing(snapshot)
            val result = watchdog.reduce(
                probing,
                WatchdogEvent.ProbeFailed(requireNotNull(probing.probeTicket)),
            )
            delays += result.actions.filterIsInstance<WatchdogAction.Schedule>().single().delayMillis
            if (zeroBasedFailure == 2) {
                assertEquals(
                    listOf(
                        WatchdogAction.ClearSession,
                        WatchdogAction.ControlledRedial(edgeIndex = 0),
                        scheduleAction(result.snapshot, 20_000),
                    ),
                    result.actions,
                )
            }
            snapshot = result.snapshot
        }

        assertEquals(listOf(5_000L, 10_000L, 20_000L, 40_000L, 80_000L, 120_000L), delays)
        assertEquals(WatchdogState.BACKING_OFF, snapshot.state)
        assertEquals(1, snapshot.edgeIndex)
        assertEquals(0, snapshot.failures)
    }

    @Test
    fun failuresAreBoundedThenFallbackToOrdinaryVpn() {
        var snapshot = onlineSnapshot(edgeCount = 1)
        repeat(5) {
            val probing = probing(snapshot)
            snapshot = watchdog.reduce(
                probing,
                WatchdogEvent.ProbeFailed(requireNotNull(probing.probeTicket)),
            ).snapshot
        }
        val probing = probing(snapshot)
        val final = watchdog.reduce(
            probing,
            WatchdogEvent.ProbeFailed(requireNotNull(probing.probeTicket)),
        )

        assertEquals(WatchdogState.FALLBACK, final.snapshot.state)
        assertEquals(
            listOf(
                WatchdogAction.CancelProbe,
                WatchdogAction.ClearSession,
                WatchdogAction.FallbackOrdinaryVpn,
            ),
            final.actions,
        )
    }

    @Test
    fun wakeAndStopDoNotLeaveAStaleProbe() {
        val probing = probing(onlineSnapshot())
        val wake = watchdog.reduce(
            probing,
            WatchdogEvent.Wake,
            jitterMillis = 2_500,
        )
        assertEquals(
            listOf(WatchdogAction.CancelProbe, scheduleAction(wake.snapshot, 2_000)),
            wake.actions,
        )

        val waiting = watchdog.reduce(
            WatchdogSnapshot.stopped(),
            WatchdogEvent.Start(edgeCount = 1),
        ).snapshot
        assertTrue(watchdog.reduce(waiting, WatchdogEvent.Wake, jitterMillis = 500).actions.isEmpty())

        val stopped = watchdog.reduce(wake.snapshot, WatchdogEvent.Stop)
        assertEquals(WatchdogState.STOPPED, stopped.snapshot.state)
        assertEquals(wake.snapshot.generation, stopped.snapshot.generation)
        assertEquals(wake.snapshot.lastNetworkEpoch, stopped.snapshot.lastNetworkEpoch)
        assertEquals(
            listOf(WatchdogAction.CancelProbe, WatchdogAction.ClearSession),
            stopped.actions,
        )
    }

    @Test
    fun startWithoutEdgesFallsBackImmediately() {
        val result = watchdog.reduce(WatchdogSnapshot.stopped(), WatchdogEvent.Start(edgeCount = 0))

        assertEquals(WatchdogState.FALLBACK, result.snapshot.state)
        assertEquals(listOf(WatchdogAction.FallbackOrdinaryVpn), result.actions)
    }

    @Test
    fun invalidPolicySnapshotEventOrderAndIntervalJitterAreRejected() {
        assertThrows(IllegalArgumentException::class.java) {
            WhiteListWatchdog(
                WhiteListWatchdogPolicy(
                    intervalMinMillis = 35_000,
                    intervalMaxMillis = 25_000,
                ),
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            watchdog.reduce(
                WatchdogSnapshot.stopped().copy(edgeIndex = 2, edgeCount = 1),
                WatchdogEvent.Stop,
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            watchdog.reduce(WatchdogSnapshot.stopped(), WatchdogEvent.Wake)
        }
        val probing = probing(onlineSnapshot())
        assertThrows(IllegalArgumentException::class.java) {
            watchdog.reduce(
                probing,
                WatchdogEvent.ProbeSucceeded(requireNotNull(probing.probeTicket)),
                jitterMillis = 24_999,
            )
        }
    }

    @Test
    fun safeLogHasNoArbitrarySecretChannel() {
        val rendered = WhiteListLogEvent(
            code = WhiteListLogCode.PROBE_FAILED,
            state = WatchdogState.BACKING_OFF,
            failureCount = 2,
            edgeOrdinal = 1,
        ).renderSafe()

        listOf("token-secret", "https://secret/sub/token", "credential-secret", "exception-secret")
            .forEach { assertFalse(rendered.contains(it)) }
        assertEquals(
            "code=PROBE_FAILED state=BACKING_OFF failures=2 edge=1",
            rendered,
        )
    }

    private fun onlineSnapshot(edgeCount: Int = 2): WatchdogSnapshot {
        val started = watchdog.reduce(
            WatchdogSnapshot.stopped(),
            WatchdogEvent.Start(edgeCount = edgeCount),
        )
        return watchdog.reduce(
            started.snapshot,
            WatchdogEvent.NetworkAvailable(epoch = 1),
            jitterMillis = 500,
        ).snapshot
    }

    private fun probing(snapshot: WatchdogSnapshot): WatchdogSnapshot = watchdog.reduce(
        snapshot,
        WatchdogEvent.Timer(requireNotNull(snapshot.scheduleTicket)),
    ).snapshot

    private fun scheduleAction(
        snapshot: WatchdogSnapshot,
        delayMillis: Long,
    ) = WatchdogAction.Schedule(
        delayMillis = delayMillis,
        ticket = requireNotNull(snapshot.scheduleTicket),
    )
}

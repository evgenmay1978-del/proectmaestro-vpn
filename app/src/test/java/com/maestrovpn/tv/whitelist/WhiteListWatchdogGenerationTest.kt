package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class WhiteListWatchdogGenerationTest {
    private val watchdog = WhiteListWatchdog()

    @Test
    fun staleTimerAfterReplacementAndDuplicateTimerAreIgnored() {
        val first = online()
        val firstSchedule = requireNotNull(first.scheduleTicket)
        val replacement = watchdog.reduce(
            first,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 700,
        )
        val replacementSchedule = requireNotNull(replacement.snapshot.scheduleTicket)

        assertNoOp(
            replacement.snapshot,
            watchdog.reduce(replacement.snapshot, WatchdogEvent.Timer(firstSchedule)),
        )

        val probing = watchdog.reduce(
            replacement.snapshot,
            WatchdogEvent.Timer(replacementSchedule),
        )
        assertEquals(WatchdogState.PROBING, probing.snapshot.state)
        assertEquals(1, probing.actions.filterIsInstance<WatchdogAction.Probe>().size)
        assertNoOp(
            probing.snapshot,
            watchdog.reduce(probing.snapshot, WatchdogEvent.Timer(replacementSchedule)),
        )
    }

    @Test
    fun staleProbeResultsAfterReplacementCannotResetOrAdvanceFailures() {
        val first = probing(online())
        val oldProbe = requireNotNull(first.probeTicket)
        val replacement = watchdog.reduce(
            first,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 400,
        )

        assertNoOp(
            replacement.snapshot,
            watchdog.reduce(
                replacement.snapshot,
                WatchdogEvent.ProbeSucceeded(oldProbe),
                jitterMillis = 30_000,
            ),
        )
        assertNoOp(
            replacement.snapshot,
            watchdog.reduce(replacement.snapshot, WatchdogEvent.ProbeFailed(oldProbe)),
        )
        assertEquals(0, replacement.snapshot.failures)
    }

    @Test
    fun oldNetworkLossIsIgnoredAfterReplacementAndWake() {
        val first = online()
        val firstNetwork = requireNotNull(first.networkTicket)
        val replacement = watchdog.reduce(
            first,
            WatchdogEvent.NetworkAvailable(epoch = 2),
            jitterMillis = 300,
        )
        val replacementNetwork = requireNotNull(replacement.snapshot.networkTicket)

        assertNoOp(
            replacement.snapshot,
            watchdog.reduce(replacement.snapshot, WatchdogEvent.NetworkLost(firstNetwork)),
        )

        val wake = watchdog.reduce(
            replacement.snapshot,
            WatchdogEvent.Wake,
            jitterMillis = 500,
        )
        val wakeNetwork = requireNotNull(wake.snapshot.networkTicket)
        assertNotEquals(replacementNetwork, wakeNetwork)
        assertEquals(replacementNetwork.epoch, wakeNetwork.epoch)
        assertTrue(wakeNetwork.generation > replacementNetwork.generation)

        assertNoOp(
            wake.snapshot,
            watchdog.reduce(wake.snapshot, WatchdogEvent.NetworkLost(replacementNetwork)),
        )
        val currentLoss = watchdog.reduce(
            wake.snapshot,
            WatchdogEvent.NetworkLost(wakeNetwork),
        )
        assertEquals(WatchdogState.WAITING_NETWORK, currentLoss.snapshot.state)
    }

    @Test
    fun wakeInvalidatesPendingTimerAndProbeResults() {
        val scheduled = online()
        val oldSchedule = requireNotNull(scheduled.scheduleTicket)
        val rescheduled = watchdog.reduce(
            scheduled,
            WatchdogEvent.Wake,
            jitterMillis = 600,
        )
        assertNoOp(
            rescheduled.snapshot,
            watchdog.reduce(rescheduled.snapshot, WatchdogEvent.Timer(oldSchedule)),
        )

        val probing = probing(rescheduled.snapshot)
        val oldProbe = requireNotNull(probing.probeTicket)
        val wake = watchdog.reduce(
            probing,
            WatchdogEvent.Wake,
            jitterMillis = 800,
        )
        assertNoOp(
            wake.snapshot,
            watchdog.reduce(
                wake.snapshot,
                WatchdogEvent.ProbeSucceeded(oldProbe),
                jitterMillis = 30_000,
            ),
        )
        assertNoOp(
            wake.snapshot,
            watchdog.reduce(wake.snapshot, WatchdogEvent.ProbeFailed(oldProbe)),
        )
    }

    private fun online(): WatchdogSnapshot {
        val started = watchdog.reduce(
            WatchdogSnapshot.stopped(),
            WatchdogEvent.Start(edgeCount = 2),
        )
        return watchdog.reduce(
            started.snapshot,
            WatchdogEvent.NetworkAvailable(epoch = 1),
            jitterMillis = 250,
        ).snapshot
    }

    private fun probing(snapshot: WatchdogSnapshot): WatchdogSnapshot = watchdog.reduce(
        snapshot,
        WatchdogEvent.Timer(requireNotNull(snapshot.scheduleTicket)),
    ).snapshot

    private fun assertNoOp(
        expected: WatchdogSnapshot,
        actual: WhiteListWatchdogResult,
    ) {
        assertEquals(expected, actual.snapshot)
        assertTrue(actual.actions.isEmpty())
    }
}

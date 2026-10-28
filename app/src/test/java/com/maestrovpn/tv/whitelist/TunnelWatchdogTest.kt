package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger
import kotlin.random.Random

/**
 * How the watchdog host behaves end to end: the pure machine is covered by WhiteListWatchdogTest,
 * this file covers the wiring — that "live" is only ever reported after a real probe, and that the
 * escalation really runs (redial -> backoff -> fallback) on the timeline we ship.
 */
class TunnelWatchdogTest {
    private val policy = WhiteListWatchdogPolicy(
        intervalMinMillis = 20,
        intervalMaxMillis = 30,
        probeTimeoutMillis = 100,
        baseBackoffMillis = 50,
        maxBackoffMillis = 100,
        redialAfterFailures = 1,
        advanceEdgeAfterFailures = 3,
    )

    private class Recorder {
        val live = AtomicInteger()
        val redials = AtomicInteger()
        val fallbacks = AtomicInteger()
        val clears = AtomicInteger()
        val probes = AtomicInteger()
        val notes = mutableListOf<String>()

        fun note(text: String) {
            synchronized(notes) { notes.add(text) }
        }

        fun redialIndices(): List<String> = synchronized(notes) { notes.filter { it.startsWith("redial:") } }
    }

    private fun newHost(rec: Recorder, answers: (attempt: Int) -> Boolean) = TunnelWatchdog(
        probe = {
            rec.probes.incrementAndGet()
            answers(rec.probes.get())
        },
        onLive = { rec.live.incrementAndGet() },
        onRedial = { edge ->
            rec.redials.incrementAndGet()
            rec.note("redial:" + edge)
        },
        onFallbackOrdinary = { rec.fallbacks.incrementAndGet() },
        onClearSession = { rec.clears.incrementAndGet() },
        log = rec::note,
        policy = policy,
        random = Random(7),
        startupJitterMax = 5,
    )

    private fun awaitTrue(timeoutMs: Long = 5_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (condition()) return
            Thread.sleep(10)
        }
        throw AssertionError("condition not reached in " + timeoutMs + "ms")
    }

    @Test
    fun liveIsReportedOnlyAfterARealProbe() {
        val rec = Recorder()
        val host = newHost(rec) { true }
        try {
            host.start(1)
            assertEquals("starting the host alone must not prove anything", 0, rec.probes.get())
            assertEquals(0, rec.live.get())
            host.networkAvailable()
            awaitTrue { rec.live.get() == 1 }
            assertEquals(0, rec.redials.get())
            assertEquals(0, rec.fallbacks.get())
        } finally {
            host.stop()
        }
    }

    @Test
    fun doesNotProbeWithoutANetwork() {
        val rec = Recorder()
        val host = newHost(rec) { true }
        try {
            host.start(1)
            Thread.sleep(150)
            assertEquals(0, rec.probes.get())
            assertEquals(0, rec.live.get())
        } finally {
            host.stop()
        }
    }

    @Test
    fun redialsAfterTheFirstFailureAndRecovers() {
        val rec = Recorder()
        val host = newHost(rec) { attempt -> attempt >= 3 }
        try {
            host.start(1)
            host.networkAvailable()
            awaitTrue { rec.live.get() == 1 }
            assertEquals(1, rec.redials.get())
            assertEquals(listOf("redial:0"), rec.redialIndices())
            assertEquals(0, rec.fallbacks.get())
        } finally {
            host.stop()
        }
    }

    @Test
    fun fallsBackWhenEveryProbeFails() {
        val rec = Recorder()
        val host = newHost(rec) { false }
        try {
            host.start(1)
            host.networkAvailable()
            awaitTrue { rec.fallbacks.get() == 1 }
            assertEquals("a dead path must never be reported as live", 0, rec.live.get())
            assertEquals(1, rec.redials.get())
            assertTrue("the session must be dropped before falling back", rec.clears.get() >= 1)
        } finally {
            host.stop()
        }
    }

    @Test
    fun stopEndsTheSchedule() {
        val rec = Recorder()
        val host = newHost(rec) { true }
        host.start(1)
        host.networkAvailable()
        awaitTrue { rec.live.get() == 1 }
        host.stop()
        Thread.sleep(120)
        val settled = rec.probes.get()
        Thread.sleep(150)
        assertEquals("nothing may be probed after stop", settled, rec.probes.get())
        assertEquals(1, rec.live.get())
    }

    @Test
    fun wakeReschedulesWithoutProvingAnything() {
        val rec = Recorder()
        val host = newHost(rec) { true }
        try {
            host.start(1)
            host.wake()
            Thread.sleep(150)
            assertEquals("wake without a network ticket is a no-op", 0, rec.probes.get())
            assertEquals(0, rec.live.get())
        } finally {
            host.stop()
        }
    }
}

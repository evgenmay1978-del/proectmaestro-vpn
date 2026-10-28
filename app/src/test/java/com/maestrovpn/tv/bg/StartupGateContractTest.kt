package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D3 contract, second half: gating "connected" behind a real probe must not recreate the endless
 * "connecting" state that D1 fixed. The probe starts within ~2 s and keeps retrying with backoff, so
 * a healthy start reports "connected" in seconds. If NO path exists at all the watchdog machine merely
 * waits for a network (state WAITING_NETWORK), so without a bound the UI would spin forever.
 *
 * The gate is a 30 s timer armed together with the watchdog, cancelled when the tunnel proves live
 * (markStarted) and whenever the watchdog is torn down.
 */
class StartupGateContractTest {
    @Test
    fun theStartupGateIsArmedWithTheWatchdog() {
        val body = startWatchdogBody(source())

        assertTrue("gate constant must exist", source().contains("STARTUP_GATE_MS = 30_000L"))
        assertTrue("watchdog start must arm the gate timer", body.contains("watchdogGate = GlobalScope.launch"))
        assertTrue("gate must wait before giving up", body.contains("delay(STARTUP_GATE_MS)"))
    }

    @Test
    fun theGateOnlyGivesUpWhileStillConnecting() {
        val body = startWatchdogBody(source())

        assertTrue(
            "gate must re-check that the service is still connecting",
            body.contains("if (status.value != Status.Starting || tunnelWatchdog == null) return@launch"),
        )
        assertTrue("gate must fall back instead of lying about the status", body.contains("fallbackOrGiveUp()"))
        assertFalse("gate must never publish Started by itself", body.contains("status.value = Status.Started"))
    }

    @Test
    fun theGateIsClearedWhenTheTunnelProvesLiveOrStops() {
        val whole = source()
        val started = section(whole, "private suspend fun markStarted()", "private suspend fun startTunnelWatchdog()")
        val stopped = section(whole, "private fun stopTunnelWatchdog()", "private suspend fun probeTunnel(")

        val gate = startWatchdogBody(whole).substringAfter("watchdogGate = GlobalScope.launch")

        assertEquals("markStarted must cancel the gate", 1, count(started, "watchdogGate?.cancel()"))
        assertEquals("teardown must cancel the gate", 1, count(stopped, "watchdogGate?.cancel()"))
        val dropped = gate.indexOf("watchdogGate = null")
        val fallback = gate.indexOf("fallbackOrGiveUp()")
        assertTrue(
            "the timer must drop its own reference before falling back ($dropped vs $fallback)",
            dropped >= 0 && fallback > dropped,
        )
    }

    /** startTunnelWatchdog() body: from its declaration to the next declaration. */
    private fun startWatchdogBody(source: String): String =
        section(source, "private suspend fun startTunnelWatchdog()", "private fun onWatchdogNetwork(")

    private fun section(source: String, from: String, to: String): String {
        val start = source.indexOf(from)
        require(start >= 0) { "missing $from — update this contract" }
        val end = source.indexOf(to, start)
        require(end > start) { "cannot delimit $from" }
        return source.substring(start, end)
    }

    private fun count(haystack: String, needle: String): Int = haystack.windowed(needle.length).count { it == needle }

    private fun source(): String = appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText()

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

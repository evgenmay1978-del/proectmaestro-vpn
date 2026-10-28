package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 1.0.179 contract, and the reason 1.0.178 is not shippable: a probe timeout is NOT proof of a dead
 * tunnel. Measured on the owner's LTE (28.10.2026) the same healthy nodes complete a TLS handshake in
 * 0.2-0.6 s most of the time and stall past 4.5 s on a sizable minority of attempts, so the 30 s
 * "startup gate" stopped a WORKING tunnel and showed "Туннель не пропускает трафик".
 *
 * The startup path may therefore publish Started when the engine accepted the config (the 1.0.176
 * behaviour) and must never stop the tunnel on probe evidence. Verification stays as a background
 * monitor whose decisions go to StartupDiag.
 */
class StartupGateContractTest {
    @Test
    fun theDestructiveGateIsGone() {
        val whole = source()

        assertFalse("no 30 s give-up timer", whole.contains("STARTUP_GATE_MS"))
        assertFalse("no gate job", whole.contains("watchdogGate"))
    }

    @Test
    fun noStartupPathStopsTheTunnelOnProbeEvidence() {
        val whole = source()

        assertFalse(
            "the 1.0.178 alert text must be gone",
            whole.contains("Туннель не пропускает трафик"),
        )
        val fallback = section(whole, "private suspend fun fallbackOrGiveUp()", "override fun serviceStop()")
        assertFalse("the fallback may not stop the service", fallback.contains("stopAndAlert"))
        assertTrue("the fallback must re-arm the watchdog", fallback.contains("startTunnelWatchdog()"))
    }

    @Test
    fun statusDoesNotWaitForTheProbe() {
        val whole = source()
        val body = section(whole, "private suspend fun startTunnelWatchdog()", "private fun onWatchdogNetwork(")

        val started = body.indexOf("markStarted()")
        val gated = body.indexOf("if (probeEndpoint == null) return")
        assertTrue("the watchdog start must publish Started ($started)", started >= 0)
        assertTrue("Started must come before the probe channel check ($started vs $gated)", gated > started)
    }

    @Test
    fun theProbeChannelStaysAMonitor() {
        val whole = source()

        assertTrue(
            "the config still carries the probe inbound",
            whole.contains("attachProbeInbound("),
        )
        assertTrue(
            "probe decisions are recorded for post-mortem",
            whole.contains("StartupDiag.log(\"probe\""),
        )
    }

    private fun section(source: String, from: String, to: String): String {
        val start = source.indexOf(from)
        require(start >= 0) { "missing $from — update this contract" }
        val end = source.indexOf(to, start)
        require(end > start) { "cannot delimit $from" }
        return source.substring(start, end)
    }

    private fun source(): String = appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText()

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

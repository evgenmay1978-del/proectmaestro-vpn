package com.maestrovpn.tv.whitelist

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D2 root, startup half: a CDN session that fails to come up must roll the tunnel back to the
 * ordinary (paid) nodes instead of stopping the whole box.
 *
 * Before this fix BoxService.applySelectedContent fell back only when there was no cellular
 * network at all:
 *   if (cdn && WhiteListSession.network() == null) { ... restoreOrdinary(...) }
 * With cellular present — the normal case — every startup failure (DNS budget under D7, bytes
 * rejected by the engine, a dead edge, the native client not starting) fell through to
 * throw IllegalStateException("CDN: подключение недоступно") → startService → stopAndAlert, so the
 * paid ordinary nodes never got a chance and Settings.startedByUser was wiped with the tunnel.
 *
 * Pinned here: the rollback decision no longer depends on the network, the rollback target still
 * comes from the original config, and ordinaryTag == null keeps the honest stop path.
 */
class CdnStartFallbackContractTest {
    private val box = appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText()
    private val boxLines = box.lines()

    @Test
    fun theCdnTeardownDecisionNoLongerDependsOnTheNetwork() {
        assertFalse(
            "a CDN failure must not be classified by the network presence — that was the defect",
            box.contains("WhiteListSession.network()"),
        )
    }

    @Test
    fun startupFailureRollsBackWheneverARollbackTargetExists() {
        val line = exactLine("                if (cdn && WhiteListSelection.account() == sourceAccount) {")
        val block = boxLines.subList(line, line + 4).joinToString("\n")
        assertTrue(
            "the catch block must resolve the rollback target from the original config",
            block.contains("val ordinaryTag = WhiteListConfig.ordinaryTag(content)"),
        )
        assertTrue(
            "the catch block must roll the selection back to that target",
            block.contains("WhiteListSelection.restoreOrdinary(requireNotNull(request), ordinaryTag) != null) return@repeat"),
        )
    }

    @Test
    fun stopPathIsKeptForAConfigWithoutOrdinaryNodes() {
        val line = exactLine("                if (cdn && WhiteListSelection.account() == sourceAccount) {")
        assertFalse(
            "ordinaryTag == null must still stop the tunnel (no silent success)",
            boxLines.subList(line, line + 4).joinToString("\n").contains("else"),
        )
        assertTrue(
            "a config without ordinary nodes still reaches the honest CDN error",
            box.contains("if (cdn) throw IllegalStateException(\"CDN: подключение недоступно\")"),
        )
    }

    @Test
    fun theRollbackStillReturnsToTheApplyLoopInsteadOfSwallowingTheFailure() {
        val line = exactLine("                if (cdn && WhiteListSelection.account() == sourceAccount) {")
        assertTrue(
            "restoreOrdinary must return@repeat so the loop re-applies the ordinary content",
            boxLines.subList(line, line + 4).joinToString("\n").contains("return@repeat"),
        )
    }

    private fun exactLine(text: String): Int {
        val matches = boxLines.withIndex().filter { it.value == text }
        assertEquals("expected exactly one line: $text", 1, matches.size)
        return matches.single().index
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

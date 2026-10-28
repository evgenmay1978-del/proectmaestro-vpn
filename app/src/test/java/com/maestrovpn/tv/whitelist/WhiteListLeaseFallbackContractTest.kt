package com.maestrovpn.tv.whitelist

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D2 contract: a lapsed CDN lease rolls the tunnel back to the ordinary (paid) nodes.
 *
 * Before the fix both expiry paths derived the rollback flag from the network:
 *   expire(live, restoreOrdinary = network() != live.network)
 * A lease lapsing on the SAME network therefore passed false, expire() forwarded a null ordinary
 * tag, BoxService.kt:107-115 took its "else" branch and stopped the whole box — killing the paid
 * ordinary nodes together with the CDN session and wiping Settings.startedByUser (no autostart
 * after reboot). The lease revokes the CDN node only; the ordinary nodes stay alive (payment gate
 * refuses only an expired subscription).
 *
 * Pinned here: the expiry timer and the renewal loop ask for the rollback unconditionally,
 * expire() only forwards the tag when that flag is set, and WhiteListConfig.ordinaryTag decides
 * whether a rollback target exists at all (null → stop is still the honest outcome).
 */
class WhiteListLeaseFallbackContractTest {
    private val session = appFile("src/main/java/com/maestrovpn/tv/whitelist/WhiteListSession.kt").readText()

    @Test
    fun expiryTimerAsksForTheOrdinaryRollbackRegardlessOfNetwork() {
        val armBody = session.substringAfter("private fun armExpiry(").substringBefore("private fun expire(")
        assertTrue("armExpiry must request the ordinary rollback (D2)", armBody.contains("restoreOrdinary = true"))
        assertFalse(
            "armExpiry must not derive the rollback from the network — that was the defect",
            armBody.contains("live.network"),
        )
    }

    @Test
    fun renewalLoopAsksForTheOrdinaryRollbackOnEveryTeardown() {
        val renewalBody = session.substringAfter("renewal = scope.launch {").substringBefore("val payload")
        assertEquals(
            "both renewal teardowns (lapsed lease and a foreign projection) must fall back to ordinary (D2)",
            2,
            Regex("expire\\(live, restoreOrdinary = true\\)").findAll(renewalBody).count(),
        )
        assertFalse(
            "the renewal loop must not derive the rollback from the network — that was the defect",
            renewalBody.contains("live.network"),
        )
    }

    @Test
    fun everySessionTeardownAsksForTheOrdinaryRollback() {
        val sites = session.lines().filter { it.contains("expire(live, restoreOrdinary") }
        assertEquals("every expire() call site is pinned here", 7, sites.size)
        sites.forEach { line ->
            assertTrue(
                "all teardown paths must request the ordinary rollback: $line",
                line.contains("restoreOrdinary = true"),
            )
        }
    }

    @Test
    fun expireForwardsTheOrdinaryTagOnlyWhenRollbackWasRequested() {
        val expireBody = session.substringAfter("private fun expire(").substringBefore("fun close()")
        assertTrue(
            "a false flag must keep yielding a null tag (the stop path stays for ordinaryTag == null)",
            expireBody.contains("onExpired(live.request, live.ordinaryTag.takeIf { restoreOrdinary })"),
        )
        assertTrue(
            "prepare() must capture the rollback target from the ORIGINAL base config",
            session.contains("WhiteListConfig.ordinaryTag(base)"),
        )
    }

    @Test
    fun ordinaryRollbackTargetIsTheSavedOrdinaryChoice() {
        val base = """
            {"outbounds":[
              {"type":"selector","tag":"select","default":"auto","outbounds":["auto","vless-1"]},
              {"type":"urltest","tag":"auto","outbounds":["vless-1"]},
              {"type":"vless","tag":"vless-1"}
            ]}
        """.trimIndent()
        assertEquals("auto", WhiteListConfig.ordinaryTag(base))
    }

    @Test
    fun noRollbackTargetWhenTheConfigIsAlreadyCdnInjected() {
        val injected = """
            {"outbounds":[
              {"type":"selector","tag":"select","default":"auto","outbounds":["auto"]},
              {"type":"vless","tag":"auto"},
              {"type":"vless","tag":"cdn:edge-1"}
            ]}
        """.trimIndent()
        assertNull("a config without ordinary nodes has nothing to roll back to", WhiteListConfig.ordinaryTag(injected))
    }

    @Test
    fun noRollbackTargetWithoutASelector() {
        val noSelector = """{"outbounds":[{"type":"vless","tag":"vless-1"}]}"""
        assertNull(WhiteListConfig.ordinaryTag(noSelector))
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

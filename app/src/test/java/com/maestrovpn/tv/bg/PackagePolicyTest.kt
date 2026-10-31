package com.maestrovpn.tv.bg

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The app must never ride its own tunnel: otherwise a dead config locks the control plane
 * (claim, /sub, OTA) out of its only recovery path.
 */
class PackagePolicyTest {
    private val app = "com.maestrovpn.tv"

    @Test
    fun defaultTunnelsEverythingExceptTheApp() {
        val d = PackagePolicy.decide(app, perAppEnabled = false, includeMode = false, appList = emptySet())
        assertTrue(d.include.isEmpty())
        assertEquals(listOf(app), d.exclude)
    }

    @Test
    fun includeModeNeverListsTheApp() {
        val d = PackagePolicy.decide(
            app,
            perAppEnabled = true,
            includeMode = true,
            appList = setOf(app, "com.google.android.youtube.tv"),
        )
        assertEquals(listOf("com.google.android.youtube.tv"), d.include)
        assertTrue(d.exclude.isEmpty())
    }

    @Test
    fun includeModeWithEmptySelectionFallsBackToExcludingTheApp() {
        val d = PackagePolicy.decide(app, perAppEnabled = true, includeMode = true, appList = emptySet())
        assertTrue(d.include.isEmpty())
        assertEquals(listOf(app), d.exclude)
    }

    @Test
    fun excludeModeAddsTheAppToTheBypassList() {
        val d = PackagePolicy.decide(
            app,
            perAppEnabled = true,
            includeMode = false,
            appList = setOf("com.example.bank"),
        )
        assertTrue(d.include.isEmpty())
        assertEquals(listOf("com.example.bank", app), d.exclude)
    }

    @Test
    fun appInTheExcludeListIsNotDuplicated() {
        val d = PackagePolicy.decide(app, perAppEnabled = true, includeMode = false, appList = setOf(app))
        assertEquals(listOf(app), d.exclude)
    }

    /**
     * Regression for the AIVPN tunnel carrying every app on the device (owner, 31.10.2026: "нужно
     * что бы только те которые отмечены в приложении"). AivpnVpnService used to hardcode
     * addDisallowedApplication(self) instead of asking this policy, so dumpsys showed the tunnel
     * uid set as everything-but-the-app. Both engines must resolve the same selection to the same
     * answer: the protocol is an implementation detail of the tunnel, not of the app list.
     */
    @Test
    fun aSelectionIsResolvedIdenticallyForEveryEngine() {
        val selection = setOf("com.example.bank", "com.example.messenger")
        val includeDecision =
            PackagePolicy.decide(app, perAppEnabled = true, includeMode = true, appList = selection)
        assertEquals(listOf("com.example.bank", "com.example.messenger"), includeDecision.include)
        assertTrue("the app itself is never tunneled", includeDecision.exclude.isEmpty())
        assertFalse("a tunnel must not carry an unticked app", includeDecision.include.contains(app))
    }

    @Test
    fun includeAndExcludeAreNeverBothSet() {
        val lists = listOf(emptySet(), setOf(app), setOf("com.example.one"), setOf(app, "com.example.two"))
        for (perApp in listOf(false, true)) {
            for (include in listOf(false, true)) {
                for (list in lists) {
                    val d = PackagePolicy.decide(app, perApp, include, list)
                    assertFalse(
                        "both lists set: perApp=$perApp include=$include list=$list",
                        d.include.isNotEmpty() && d.exclude.isNotEmpty(),
                    )
                }
            }
        }
    }
}

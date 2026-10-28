package com.maestrovpn.tv.utils

import com.maestrovpn.tv.whitelist.WhiteListWatchdogPolicy
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The probe budget is the reason 1.0.178 stopped a working tunnel: 9 s split over two targets gives
 * 4.5 s per handshake, and the owner's LTE needs more than that on a minority of attempts. These are
 * the numbers the watchdog and the probe must agree on.
 */
class TunnelProbeBudgetTest {
    @Test
    fun theBudgetIsSplitBetweenTheTargets() {
        assertEquals(10_000, TunnelProbe.perTargetBudget(TunnelProbe.TIMEOUT_MS, 2))
    }

    @Test
    fun aSlowHandshakeAlwaysGetsTenSeconds() {
        assertEquals(TunnelProbe.MIN_TARGET_MS, TunnelProbe.perTargetBudget(1_000, 2))
        assertEquals(TunnelProbe.MIN_TARGET_MS, TunnelProbe.perTargetBudget(9_000, 2))
        assertTrue(TunnelProbe.MIN_TARGET_MS >= 10_000)
    }

    @Test
    fun anEmptyTargetListDoesNotDivideByZero() {
        assertEquals(TunnelProbe.MIN_TARGET_MS, TunnelProbe.perTargetBudget(20_000, 0))
    }

    @Test
    fun theWatchdogPolicyCarriesTheSameBudget() {
        assertTrue(TunnelProbe.TIMEOUT_MS >= 20_000)
        assertEquals(TunnelProbe.TIMEOUT_MS.toLong(), WhiteListWatchdogPolicy().probeTimeoutMillis)
    }
}

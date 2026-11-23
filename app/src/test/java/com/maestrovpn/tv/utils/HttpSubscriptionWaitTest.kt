package com.maestrovpn.tv.utils

import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.whitelist.TunnelFetch
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Test

class HttpSubscriptionWaitTest {
    @After
    fun clearPort() {
        TunnelFetch.clear()
    }

    private val canonical = "${BuildConfig.BACKEND_URL.trimEnd('/')}/sub/token"

    @Test
    fun canonicalWithoutProxyKeepsTheOldBudget() {
        assertEquals(12_000L, subscriptionWaitMs(canonical, 6_000))
        assertEquals(30_000L, subscriptionWaitMs(canonical, 15_000))
    }

    @Test
    fun canonicalWithProxyAddsOneProxyAttempt() {
        TunnelFetch.proxyPort = 1
        try {
            // Old budget (2 x timeoutMs) + the worst proxy attempt (3 s connect + 3 s read):
            // the proxy must not eat the budget the direct and CDN attempts had.
            assertEquals(18_000L, subscriptionWaitMs(canonical, 6_000))
            assertEquals(36_000L, subscriptionWaitMs(canonical, 15_000))
        } finally {
            TunnelFetch.clear()
        }
    }

    @Test
    fun foreignUrlNeverAddsProxyBudget() {
        val foreign = "https://other.example/sub/token"
        assertEquals(15_000L, subscriptionWaitMs(foreign, 15_000))
        TunnelFetch.proxyPort = 1
        try {
            // The proxy is only attempted for canonical subscriptions, so a foreign URL
            // keeps its plain timeout even while the tunnel is up.
            assertEquals(15_000L, subscriptionWaitMs(foreign, 15_000))
        } finally {
            TunnelFetch.clear()
        }
    }
}

package com.maestrovpn.tv.utils

import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SubscriptionHealthTest {
    @After
    fun tearDown() {
        SubscriptionHealth.clear()
    }

    @Test
    fun expiredStatusRaisesExpired() {
        assertEquals(SubscriptionHealth.Kind.EXPIRED, SubscriptionHealth.classify(402))
    }

    @Test
    fun goneStatusRaisesRevoked() {
        assertEquals(SubscriptionHealth.Kind.REVOKED, SubscriptionHealth.classify(404))
        assertEquals(SubscriptionHealth.Kind.REVOKED, SubscriptionHealth.classify(410))
    }

    @Test
    fun transientAndSuccessStaySilent() {
        for (status in listOf(0, 200, 204, 301, 403, 429, 500, 502, 503, 504)) {
            assertNull("status $status must not alert", SubscriptionHealth.classify(status))
        }
    }

    @Test
    fun reportPublishesAndClearRemoves() {
        SubscriptionHealth.report(404)
        assertEquals(SubscriptionHealth.Alert(SubscriptionHealth.Kind.REVOKED), SubscriptionHealth.alert.value)
        SubscriptionHealth.clear()
        assertNull(SubscriptionHealth.alert.value)
    }

    @Test
    fun reportIgnoresTransientStatus() {
        SubscriptionHealth.report(503)
        assertNull(SubscriptionHealth.alert.value)
    }
}

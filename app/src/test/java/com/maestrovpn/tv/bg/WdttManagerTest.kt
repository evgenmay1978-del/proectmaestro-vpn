package com.maestrovpn.tv.bg

import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WdttManagerTest {
    @Test fun acceptsValidCredentials() {
        assertNotNull(WdttManager.validateCreds(
            "vpn.example.test:56000", listOf("callHash_1"), "password-123456", 18,
            "android-arm64", listOf("123456", "987654"), "audio",
        ))
    }

    @Test fun rejectsInjectionAndInvalidRanges() {
        assertNull(WdttManager.validateCreds(
            "vpn.example.test:70000", listOf("hash\nEVIL=1"), "short", 0,
            "fingerprint", listOf("not-numeric"), "audio;bad",
        ))
    }

    @Test fun readinessRequiresStructuredReadyEvent() {
        assertTrue(WdttManager.isReadyEvent("__WDTT_EVENT__|READY|workers=18"))
        assertFalse(WdttManager.isReadyEvent("READY"))
        assertFalse(WdttManager.isReadyEvent("__WDTT_EVENT__|NOT_READY|reason=test"))
    }
}

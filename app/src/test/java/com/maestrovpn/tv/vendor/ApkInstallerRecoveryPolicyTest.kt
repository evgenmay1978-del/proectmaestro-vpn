package com.maestrovpn.tv.vendor

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ApkInstallerRecoveryPolicyTest {
    @Test
    fun failedInstallRestoresTunnelFromIntentCapturedBeforeStop() {
        assertTrue(
            shouldRestoreTunnelAfterInstallFailure(
                weStoppedTunnel = true,
                wasStartedByUserBeforeStop = true,
            ),
        )
        assertFalse(
            shouldRestoreTunnelAfterInstallFailure(
                weStoppedTunnel = true,
                wasStartedByUserBeforeStop = false,
            ),
        )
        assertFalse(
            shouldRestoreTunnelAfterInstallFailure(
                weStoppedTunnel = false,
                wasStartedByUserBeforeStop = true,
            ),
        )
    }
}

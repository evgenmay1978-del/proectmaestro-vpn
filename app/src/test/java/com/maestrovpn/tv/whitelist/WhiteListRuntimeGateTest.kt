package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WhiteListRuntimeGateTest {
    @Test
    fun tvAndAbsentPolicyNeverEnableRuntime() {
        assertFalse(WhiteListRuntimeGate.enabled(isTelevision = true, model = activeModel()))
        assertFalse(WhiteListRuntimeGate.enabled(isTelevision = false, model = null))
    }

    @Test
    fun onlyEligibleMobilePolicyEnablesRuntime() {
        assertTrue(WhiteListRuntimeGate.enabled(isTelevision = false, model = activeModel()))
        assertFalse(WhiteListRuntimeGate.enabled(isTelevision = false, model = suspendedModel()))
        assertFalse(
            WhiteListRuntimeGate.enabled(
                isTelevision = false,
                model = activeModel().copy(heartbeatEnabled = false),
            ),
        )
    }

    private fun activeModel() = WhiteListDisplayModel(
        state = WhiteListState.ACTIVE,
        transportProfileId = "profile-a",
        transportReleaseId = "release-a",
        preset = "MAESTRO_ADVANCED",
        billingState = WhiteListBillingState.SHADOW,
        usageBytes = 0,
        remainingLimitBytes = 1,
        suspensionReason = null,
        edgeIds = listOf("edge-a"),
        heartbeatEnabled = true,
    )

    private fun suspendedModel() = activeModel().copy(state = WhiteListState.SUSPENDED)
}

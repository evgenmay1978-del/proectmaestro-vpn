package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

class AccountInfoRefreshPolicyTest {
    private val owner = AccountInfo(
        login = "wapmix",
        daysLeft = 31,
        hasSubProfile = true,
        expiresDate = "13.09.2026",
        subscriptionIdentity = "profile-7:owner-token-hash",
    )

    @Test
    fun failedRefreshKeepsLastKnownOwnerOnlyForSameTrustedSubscriptionOnMobile() {
        val failedRefresh = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = owner.subscriptionIdentity,
        )

        assertEquals(owner, accountInfoAfterRefresh(owner, failedRefresh, preserveVerifiedIdentityOnOutage = true))
    }

    @Test
    fun replacingOwnerSubscriptionDoesNotKeepPrivateOwnerIdentity() {
        val replacement = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = "profile-8:ordinary-token-hash",
        )

        assertEquals(replacement, accountInfoAfterRefresh(owner, replacement, preserveVerifiedIdentityOnOutage = true))
    }

    @Test
    fun unrelatedLocalSubscriptionDoesNotKeepPrivateOwnerIdentity() {
        val noTrustedProfile = AccountInfo(hasSubProfile = true)

        assertEquals(noTrustedProfile, accountInfoAfterRefresh(owner, noTrustedProfile, preserveVerifiedIdentityOnOutage = true))
    }

    @Test
    fun tvNeverKeepsOwnerIdentityAcrossInfoFailure() {
        val failedRefresh = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = owner.subscriptionIdentity,
        )

        assertEquals(failedRefresh, accountInfoAfterRefresh(owner, failedRefresh, preserveVerifiedIdentityOnOutage = false))
    }

    @Test
    fun removingLocalSubscriptionClearsLastKnownIdentity() {
        assertEquals(
            AccountInfo(),
            accountInfoAfterRefresh(owner, AccountInfo(), preserveVerifiedIdentityOnOutage = true),
        )
    }
}

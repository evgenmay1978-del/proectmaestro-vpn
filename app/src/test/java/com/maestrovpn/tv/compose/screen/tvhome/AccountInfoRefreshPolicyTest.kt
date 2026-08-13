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

        assertEquals(owner, accountInfoAfterRefresh(owner, failedRefresh, allowPreviousIdentityFallback = true))
    }

    @Test
    fun replacingOwnerSubscriptionDoesNotKeepPrivateOwnerIdentity() {
        val replacement = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = "profile-8:ordinary-token-hash",
        )

        assertEquals(replacement, accountInfoAfterRefresh(owner, replacement, allowPreviousIdentityFallback = true))
    }

    @Test
    fun unrelatedLocalSubscriptionDoesNotKeepPrivateOwnerIdentity() {
        val noTrustedProfile = AccountInfo(hasSubProfile = true)

        assertEquals(noTrustedProfile, accountInfoAfterRefresh(owner, noTrustedProfile, allowPreviousIdentityFallback = true))
    }

    @Test
    fun tvNeverKeepsOwnerIdentityAcrossInfoFailure() {
        val failedRefresh = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = owner.subscriptionIdentity,
        )

        assertEquals(failedRefresh, accountInfoAfterRefresh(owner, failedRefresh, allowPreviousIdentityFallback = false))
    }

    @Test
    fun authoritativeResponseWithoutLoginNeverRestoresPreviousOwner() {
        val authoritative = AccountInfo(
            hasSubProfile = true,
            subscriptionIdentity = owner.subscriptionIdentity,
        )

        assertEquals(
            authoritative,
            accountInfoAfterRefresh(owner, authoritative, allowPreviousIdentityFallback = false),
        )
    }

    @Test
    fun tvOutageDoesNotClearPrivateTransportRuntimeBaseline() {
        assertEquals(false, shouldClearPrivateTransportCreds(isMobile = false, credentialsMatch = false))
    }

    @Test
    fun mobileClearsPrivateTransportOnlyWhenSubscriptionIdentityDoesNotMatch() {
        assertEquals(false, shouldClearPrivateTransportCreds(isMobile = true, credentialsMatch = true))
        assertEquals(true, shouldClearPrivateTransportCreds(isMobile = true, credentialsMatch = false))
    }

    @Test
    fun removingLocalSubscriptionClearsLastKnownIdentity() {
        assertEquals(
            AccountInfo(),
            accountInfoAfterRefresh(owner, AccountInfo(), allowPreviousIdentityFallback = true),
        )
    }
}

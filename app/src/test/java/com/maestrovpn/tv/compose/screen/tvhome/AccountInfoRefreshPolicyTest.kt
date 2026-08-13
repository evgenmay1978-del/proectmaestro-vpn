package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Test

class AccountInfoRefreshPolicyTest {
    @Test
    fun failedRefreshKeepsLastKnownOwnerIdentityWhenLocalProfileStillExists() {
        val previous = AccountInfo(
            login = "wapmix",
            daysLeft = 31,
            hasSubProfile = true,
            expiresDate = "13.09.2026",
        )
        val failedRefresh = AccountInfo(hasSubProfile = true)

        assertEquals(previous, accountInfoAfterRefresh(previous, failedRefresh))
    }

    @Test
    fun removingLocalSubscriptionClearsLastKnownIdentity() {
        val previous = AccountInfo(
            login = "wapmix",
            daysLeft = 31,
            hasSubProfile = true,
            expiresDate = "13.09.2026",
        )

        assertEquals(AccountInfo(), accountInfoAfterRefresh(previous, AccountInfo()))
    }
}

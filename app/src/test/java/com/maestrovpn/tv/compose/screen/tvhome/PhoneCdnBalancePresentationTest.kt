package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.whitelist.WhiteListBalance
import org.junit.Assert.assertEquals
import org.junit.Test

class PhoneCdnBalancePresentationTest {
    private val fundedBalance = WhiteListBalance(
        includedRemainingBytes = 5_000_000_000,
        purchasedRemainingBytes = 14_990_000_000,
        availableBytes = 19_990_000_000,
        periodEndsAtUnix = 1_800_000_000,
        primaryAccessState = "active",
        publicationVerdict = "SIDECAR_UNAVAILABLE",
    )

    @Test
    fun positiveBalanceRemainsVisibleWhenSidecarIsUnavailable() {
        val account = PhoneCdnAccount(balance = fundedBalance, loading = false)

        assertEquals("19,99 ГБ", phoneCdnBalanceText(hasSubProfile = true, account = account))
    }

    @Test
    fun missingOrFailedFetchNeverBecomesZero() {
        assertEquals("Загрузка…", phoneCdnBalanceText(true, PhoneCdnAccount()))
        assertEquals("Недоступен", phoneCdnBalanceText(true, PhoneCdnAccount(loading = false)))
        assertEquals("Недоступен", phoneCdnBalanceText(true,
            PhoneCdnAccount(loading = false, unavailable = true)))
    }

    @Test
    fun pendingProjectionDoesNotPresentItsBytesAsAnAvailableBalance() {
        for (verdict in listOf("PROJECTION_PENDING", "PROJECTION_STALE")) {
            val account = PhoneCdnAccount(
                balance = fundedBalance.copy(publicationVerdict = verdict),
                loading = false,
            )
            assertEquals("Обновляется…", phoneCdnBalanceText(true, account))
        }
    }

    @Test
    fun expiredSubscriptionShowsPurchasedBytesAsFrozen() {
        val account = PhoneCdnAccount(
            balance = fundedBalance.copy(
                availableBytes = 0,
                primaryAccessState = "expired",
                publicationVerdict = "PRIMARY_EXPIRED",
            ),
            loading = false,
        )

        assertEquals("14,99 ГБ заморожено", phoneCdnBalanceText(true, account))
    }
}

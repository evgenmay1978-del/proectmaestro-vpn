package com.maestrovpn.tv.vendor

import org.junit.Assert.assertEquals
import org.junit.Test

class InstallConfirmationDeliveryTest {
    @Test
    fun foregroundConfirmationIsLaunchedOnceAndNeverParked() {
        var launches = 0
        var parks = 0

        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = true,
            launch = { launches++ },
            park = { parks++ },
        )

        assertEquals(InstallConfirmationDelivery.Launched, result)
        assertEquals(1, launches)
        assertEquals(0, parks)
    }

    @Test
    fun backgroundConfirmationIsParkedAndNeverLaunched() {
        var launches = 0
        var parks = 0

        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = false,
            launch = { launches++ },
            park = { parks++ },
        )

        assertEquals(InstallConfirmationDelivery.Parked, result)
        assertEquals(0, launches)
        assertEquals(1, parks)
    }

    @Test
    fun receiverLaunchFailureParksOnce() {
        var launches = 0
        var parks = 0

        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = true,
            launch = {
                launches++
                error("blocked")
            },
            park = { parks++ },
        )

        assertEquals(InstallConfirmationDelivery.Parked, result)
        assertEquals(1, launches)
        assertEquals(1, parks)
    }

    @Test
    fun coldBackgroundIsNotEligibleToLaunchConfirmation() {
        assertEquals(false, isInstallConfirmationForeground(false, false))
    }

    @Test
    fun warmBackgroundWithStartedProcessAndNoResumedActivityIsNotEligibleToLaunchConfirmation() {
        assertEquals(false, isInstallConfirmationForeground(true, false))
    }

    @Test
    fun resumedActivityIsEligibleToLaunchConfirmation() {
        assertEquals(true, isInstallConfirmationForeground(true, true))
    }

    @Test
    fun mainActivityRetryLaunchFailureReParksOnce() {
        var launches = 0
        var parks = 0

        val result = deliverInstallConfirmation(
            confirmation = "confirm",
            appInForeground = true,
            launch = {
                launches++
                error("blocked")
            },
            park = { parks++ },
        )

        assertEquals(InstallConfirmationDelivery.Parked, result)
        assertEquals(1, launches)
        assertEquals(1, parks)
    }
}

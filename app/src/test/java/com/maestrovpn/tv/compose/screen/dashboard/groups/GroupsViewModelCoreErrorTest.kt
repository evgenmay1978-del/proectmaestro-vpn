package com.maestrovpn.tv.compose.screen.dashboard.groups

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class GroupsViewModelCoreErrorTest {

    @Test
    fun rawLibboxChatterNeverReachesTheClientDialog() {
        // The exact text the client saw on 15.11.2026 (filatovlug, 1.0.203).
        assertTrue(
            isUnhelpfulCoreError("select outbound: rpc error: code = Unknown desc = invalid argument")
        )
        assertTrue(isUnhelpfulCoreError("rpc error: code = Unknown desc = not found"))
        assertTrue(isUnhelpfulCoreError("NOT FOUND"))
    }

    @Test
    fun realFailuresStillSurface() {
        assertFalse(isUnhelpfulCoreError(null))
        assertFalse(isUnhelpfulCoreError("Сервер vless-s4 недоступен"))
        assertFalse(isUnhelpfulCoreError("probe command server failed"))
    }
}

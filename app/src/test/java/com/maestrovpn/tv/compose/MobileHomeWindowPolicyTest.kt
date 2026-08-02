package com.maestrovpn.tv.compose

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MobileHomeWindowPolicyTest {
    @Test
    fun onlyPhoneHomeOwnsTheFullEdgeToEdgeWindow() {
        assertTrue(mobileHomeUsesFullWindow(isTelevision = false, isHomeRoute = true))
        assertFalse(mobileHomeUsesFullWindow(isTelevision = true, isHomeRoute = true))
        assertFalse(mobileHomeUsesFullWindow(isTelevision = false, isHomeRoute = false))
    }
}

package com.maestrovpn.tv.bg

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WdttVpnPolicyTest {
    private val appPackage = "com.maestrovpn.tv"

    @Test fun detectsOnlyTopLevelWdttOutbound() {
        assertTrue(WdttVpnPolicy.hasWdttOutbound("""{"outbounds":[{"type":"wireguard","tag":"vk-turn"}]}"""))
        assertFalse(WdttVpnPolicy.hasWdttOutbound("""{"note":"vk-turn","outbounds":[{"tag":"vless"}]}"""))
        assertFalse(WdttVpnPolicy.hasWdttOutbound("not-json"))
    }

    @Test fun excludesOwnPackageWhenWdttUsesDefaultRouting() {
        val result = WdttVpnPolicy.resolvePackageOverrides(false, false, emptySet(), appPackage, true)
        assertNull(result.include)
        assertEquals(setOf(appPackage), result.exclude)
    }

    @Test fun removesOwnPackageFromPerAppIncludeMode() {
        val result = WdttVpnPolicy.resolvePackageOverrides(
            true, true, setOf("video.app", appPackage), appPackage, true,
        )
        assertEquals(setOf("video.app"), result.include)
        assertNull(result.exclude)
    }

    @Test fun addsOwnPackageToPerAppExcludeMode() {
        val result = WdttVpnPolicy.resolvePackageOverrides(
            true, false, setOf("bank.app"), appPackage, true,
        )
        assertNull(result.include)
        assertEquals(setOf("bank.app", appPackage), result.exclude)
    }

    @Test fun preservesExistingBehaviorWithoutWdtt() {
        val include = WdttVpnPolicy.resolvePackageOverrides(
            true, true, setOf("video.app"), appPackage, false,
        )
        assertEquals(setOf("video.app", appPackage), include.include)

        val exclude = WdttVpnPolicy.resolvePackageOverrides(
            true, false, setOf("bank.app", appPackage), appPackage, false,
        )
        assertEquals(setOf("bank.app"), exclude.exclude)
    }
}

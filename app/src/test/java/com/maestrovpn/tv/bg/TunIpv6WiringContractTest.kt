package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The IPv6 repair only helps if it runs on the config the ENGINE receives, before that config is
 * validated and handed over. Both facts are pinned here: the ensure step sits in front of the probe
 * inbound, and the engine's verdict is taken on the rewritten bytes, not on the original ones.
 */
class TunIpv6WiringContractTest {
    @Test
    fun theConfigIsMadeIpv6CapableBeforeTheEngineSeesIt() {
        val apply = section(source(), "val effective = WhiteListConfig.stripAwgDecoys", "commandServer.startOrReloadService")

        assertTrue("ensure must run in the apply path", apply.contains("TunIpv6.ensure(effective)"))
        assertTrue("the engine must receive the rewritten config", apply.contains("attachProbeInbound(withIpv6)"))
        assertTrue(
            "the probe entry must be attached to the IPv6-capable config",
            apply.indexOf("TunIpv6.ensure(effective)") < apply.indexOf("attachProbeInbound(withIpv6)"),
        )
    }

    @Test
    fun theChangeIsVisibleInTheLog() {
        val apply = section(source(), "val effective = WhiteListConfig.stripAwgDecoys", "commandServer.startOrReloadService")

        assertTrue("an injected IPv6 address must be logged", apply.contains("tun IPv6: injected"))
        assertTrue("the log must not run when nothing changed", apply.contains("if (withIpv6 !== effective)"))
    }

    @Test
    fun theTunReportsTheAddressFamiliesItCaptured() {
        val openTun = section(vpnSource(), "override fun openTun(options: TunOptions): Int", "if (options.autoRoute)")

        assertTrue("the captured IPv6 addresses must be counted", openTun.contains("inet6Count++"))
        assertTrue("the count must reach the log", openTun.contains("tun families: inet6="))
        assertEquals(
            "the MTU clamp must stay untouched",
            1,
            count(openTun, "val tunMtu = options.mtu.let { if (it in 1..1280) it else 1280 }"),
        )
    }

    private fun section(source: String, from: String, to: String): String {
        val start = source.indexOf(from)
        require(start >= 0) { "missing $from — update this contract" }
        val end = source.indexOf(to, start)
        require(end > start) { "cannot delimit $from" }
        return source.substring(start, end)
    }

    private fun count(haystack: String, needle: String): Int = haystack.windowed(needle.length).count { it == needle }

    private fun source(): String = appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText()

    private fun vpnSource(): String = appFile("src/main/java/com/maestrovpn/tv/bg/VPNService.kt").readText()

    private fun appFile(path: String): File {
        val found = listOf(File(path), File("app", path)).filter(File::isFile)
        require(found.size == 1) { "expected one app file for " + path + ", found " + found.size }
        return found.single()
    }
}

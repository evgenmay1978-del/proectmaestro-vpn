package com.maestrovpn.tv.utils

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The liveness probe is worthless unless it dials the exact socket the probe inbound listens on.
 *
 * It did not: until 1.0.180 Socks5Tunnel dialled InetAddress.getLoopbackAddress(), which on Android is
 * the IPv6 loopback (::1) while the IPv6 stack is up, while ProbeInbound listens on the IPv4 literal
 * 127.0.0.1. On the owner's device (1.0.179, 28.10.2026) that produced "probe failed failure=connect
 * elapsed=1ms" on every watchdog cycle, a config re-apply every ~25 s, and no working proof at all that
 * the tunnel carries traffic.
 *
 * The dial address and the listen address must therefore stay the SAME IPv4 literal, and no main
 * source may fall back to the system loopback helper again. Unit tests run on the JVM, where that
 * helper happens to return 127.0.0.1, so only a source-level contract can catch the regression.
 */
class ProbeLoopbackContractTest {
    @Test
    fun theMainSourcesNeverDialTheSystemLoopbackAddress() {
        val sources = sources()
        assertTrue("the scan must see the app sources, saw " + sources.size, sources.size > 150)
        val offenders = sources.filter { file -> codeLines(file).any { it.contains(SYSTEM_LOOPBACK) } }
            .map { it.relativeTo(moduleDir()).path }
        assertTrue(
            "on Android " + SYSTEM_LOOPBACK + " is the IPv6 loopback (::1) and never reaches the " +
                "127.0.0.1 listeners: " + offenders,
            offenders.isEmpty(),
        )
    }

    @Test
    fun theDialTargetAndTheListenerAreTheSameIpv4Literal() {
        val client = moduleDir().resolve("src/main/java/com/maestrovpn/tv/utils/Socks5Tunnel.kt")
        val listener = moduleDir().resolve("src/main/java/com/maestrovpn/tv/whitelist/ProbeInbound.kt")
        assertTrue("dial target must be the IPv4 literal", client.readText().contains(IPV4_LOOPBACK))
        assertTrue(
            "the probe inbound must keep listening on the same IPv4 literal",
            listener.readText().contains("""put("listen", "127.0.0.1")"""),
        )
    }

    /**
     * The contract is about code, not prose: the KDoc in Socks5Tunnel that explains why the helper is
     * banned has to name it. A call cannot live on a line that begins with a comment marker, while a
     * call followed by a trailing comment ("...getLoopbackAddress() // why") stays a code line and is
     * still caught.
     */
    private fun codeLines(file: File): List<String> =
        file.readLines().map { it.trim() }
            .filterNot { it.startsWith("//") || it.startsWith("*") || it.startsWith("/*") }

    private fun sources(): List<File> =
        moduleDir().resolve("src/main").walkTopDown()
            .onEnter { it.name != "build" }
            .filter { it.isFile && it.extension.lowercase() in SOURCE_EXTENSIONS }
            .toList()

    private fun moduleDir(): File =
        listOf(File("."), File("app"))
            .firstOrNull { File(it, "src/main/java/com/maestrovpn/tv/utils/Socks5Tunnel.kt").isFile }
            ?: error("cannot locate the app module from " + File(".").absolutePath)

    private companion object {
        const val SYSTEM_LOOPBACK = "getLoopback" + "Address"
        const val IPV4_LOOPBACK = "127.0.0.1"
        val SOURCE_EXTENSIONS = setOf("kt", "java")
    }
}

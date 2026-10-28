package com.maestrovpn.tv.whitelist

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The pinned engine (sing-box v1.14.0-alpha.31) removed the per-family tun address keys: they are
 * documented as deprecated, merged into the single `address` list, and
 * protocol/tun/inbound.go:61-67 refuses the WHOLE config while any of them is set. That is exactly
 * what broke 1.0.177 (the config came back from the app with such a key and the engine answered
 * "legacy tun address fields are deprecated in sing-box 1.10.0 and removed in sing-box 1.12.0").
 *
 * Unit tests cannot run the Go engine, so this contract pins the class of defect at the source level:
 * no file under app/src may mention any of those keys, in code, test data or resources.
 */
class SingBoxRemovedTunFieldsContractTest {
    @Test
    fun theAppSourcesNeverMentionTheRemovedTunAddressKeys() {
        val module = moduleDir()
        val sources = sources(module)
        assertTrue("the scan must see the module sources, saw " + sources.size, sources.size > 150)

        val offenders = offenders(sources, module)
        assertTrue(
            "the pinned engine rejects the whole config while any of these keys is present: " + offenders,
            offenders.isEmpty(),
        )
    }

    /** Proves the guard is not vacuous: a file carrying such a key is reported. */
    @Test
    fun theGuardItselfCatchesSuchAKey() {
        val file = File.createTempFile("tun-address-guard", ".json")
        try {
            file.writeText("{\"type\":\"tun\",\"" + inet6AddressKey + "\":\"fd00::1/64\"}")
            val found = offenders(listOf(file), file.parentFile)
            assertEquals("the guard must report the planted key", 1, found.size)
            assertTrue("the report must name the key", found.single().endsWith(inet6AddressKey))
        } finally {
            file.delete()
        }
    }

    /**
     * The six json keys removed from option/tun.go: per-family addresses, routes and route excludes.
     * Assembled from parts so this file never contains one of them verbatim.
     */
    private val inet6AddressKey: String = "inet6" + "_" + "address"

    private val removedKeys: List<String> = listOf("inet4", "inet6").flatMap { family ->
        listOf("", "route_", "route_exclude_").map { prefix -> family + "_" + prefix + "address" }
    }

    private fun offenders(files: List<File>, root: File): List<String> = files.flatMap { file ->
        val text = file.readText()
        removedKeys.filter { text.contains(it) }.map { key -> file.relativeTo(root).path + ": " + key }
    }

    private fun sources(module: File): List<File> =
        module.resolve("src").walkTopDown()
            .onEnter { it.name != "build" }
            .filter { it.isFile && it.extension.lowercase() in SOURCE_EXTENSIONS }
            .toList()

    private fun moduleDir(): File =
        listOf(File("."), File("app"))
            .firstOrNull { File(it, "src/main/java/com/maestrovpn/tv/whitelist/TunIpv6.kt").isFile }
            ?: error("cannot locate the app module from " + File(".").absolutePath)

    private companion object {
        val SOURCE_EXTENSIONS = setOf("kt", "java", "kts", "json", "xml")
    }
}

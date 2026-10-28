package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * The gate the 28.10.2026 regression slipped through.
 *
 * 404 unit tests stayed green while the bytes handed to the engine were rejected outright: the
 * builder appended a per-family tun key the pinned sing-box removes as a whole config, so no tunnel
 * came up on any device. No test had ever asked the ENGINE.
 *
 * These tests push the real post-processing chain of BoxService.applySelectedContent through the
 * official binary of the pinned SINGBOX_REF (version.properties) and require exit code 0:
 *   ordinary: stripAwgDecoys -> TunIpv6.ensure -> ProbeInbound.attach
 *   cdn:      WhiteListConfig.inject -> stripAwgDecoys -> TunIpv6.ensure -> ProbeInbound.attach
 * A negative control MUST fail with the exact legacy text — a gate that cannot reproduce the defect
 * would prove nothing.
 *
 * Engine lookup: system property sbx.bin, then SINGBOX_BIN, then <repo>/.sbx/sing-box. With none of
 * those present the gate provisions the pinned release itself when CI=true or SINGBOX_REQUIRE=1 and
 * fails hard if it cannot: an unverifiable release must not pass. Elsewhere (a developer loop) the
 * tests skip and stay fast.
 */
class SingBoxEngineGateTest {
    @Test
    fun theEngineIsThePinnedRevision() {
        val engine = requireEngine()
        val (code, output) = run(engine, "version")
        assertEquals("sing-box version must exit 0:\n" + output, 0, code)
        val ref = pinnedRef()
        assertNotNull("version.properties must carry SINGBOX_REF", ref)
        assertTrue(
            "the gate must run the pinned " + ref + ", not whatever sits on PATH:\n" + output,
            output.contains(ref!!.removePrefix("v")),
        )
    }

    @Test
    fun theOrdinaryApplyChainIsAccepted() {
        val engine = requireEngine()
        val applied = ProbeInbound.attach(TunIpv6.ensure(WhiteListConfig.stripAwgDecoys(fixture)))
        assertNotNull("the probe inbound must attach to a recognised config", applied)
        assertAccepted(engine, applied!!.config, "ordinary")
    }

    @Test
    fun theCdnApplyChainIsAccepted() {
        val engine = requireEngine()
        val injected = WhiteListConfig.inject(fixture, cdnRoute, cdnRoute.address, SOCKS_PORT, CDN_USER, CDN_PASS)
        assertTrue("the CDN variant must carry the injected tag", injected.contains(cdnRoute.tag))
        val applied = ProbeInbound.attach(TunIpv6.ensure(WhiteListConfig.stripAwgDecoys(injected)))
        assertNotNull("the probe inbound must attach to the CDN variant", applied)
        assertAccepted(engine, applied!!.config, "cdn")
    }

    @Test
    fun aRemovedFamilyKeyIsRejectedWithTheLegacyError() {
        val engine = requireEngine()
        val (code, output) = check(engine, withRemovedFamilyKey(fixture), "negative")
        assertTrue("the engine must reject the removed key, rc=" + code + " output:\n" + output, code != 0)
        assertTrue(
            "the negative control must fail with the legacy text, output:\n" + output,
            output.contains(LEGACY_ERROR),
        )
    }

    private fun assertAccepted(engine: File, config: String, label: String) {
        val (code, output) = check(engine, config, label)
        assertEquals("'" + label + "' must be accepted by the engine, output:\n" + output, 0, code)
    }

    /** Runs "check" and refuses a usage dump: a wrong flag order exits 0 and proves nothing. */
    private fun check(engine: File, config: String, label: String): Pair<Int, String> {
        val file = File.createTempFile("sbx-gate-" + label + "-", ".json")
        try {
            file.writeText(config)
            val (code, output) = run(engine, "check", "--disable-color", "-c", file.absolutePath)
            assertTrue(
                "the engine was not asked to validate '" + label + "' — it printed usage:\n" + output,
                !output.contains("Usage:"),
            )
            return code to output
        } finally {
            file.delete()
        }
    }

    private fun run(engine: File, vararg arguments: String): Pair<Int, String> {
        val process = ProcessBuilder(listOf(engine.absolutePath) + arguments).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().use { it.readText() }
        assertTrue("sing-box did not finish in 120 s", process.waitFor(120, TimeUnit.SECONDS))
        return process.exitValue() to output
    }

    private fun requireEngine(): File {
        engine()?.let { return it }
        val message = "no sing-box binary: sbx.bin/SINGBOX_BIN unset, " + ".sbx/sing-box absent"
        if (System.getenv("SINGBOX_REQUIRE") == "1" || System.getenv("CI") == "true") {
            throw AssertionError(message + " and the pinned release could not be provisioned")
        }
        assumeTrue(message, false)
        return engine()!!
    }

    private fun engine(): File? {
        val explicit = listOf(System.getProperty("sbx.bin"), System.getenv("SINGBOX_BIN"))
            .filterNotNull()
            .filter { it.isNotBlank() }
            .map { File(it) }
            .firstOrNull { it.isFile }
        if (explicit != null) return explicit
        val cached = repoRoot()?.resolve(".sbx/sing-box")
        if (cached != null && cached.isFile) return cached
        return if (System.getenv("CI") == "true" || System.getenv("SINGBOX_REQUIRE") == "1") provision() else null
    }

    /**
     * Fetches the official release of the pinned ref into .sbx/. CI has to prove the config with the
     * engine itself; a gate that silently degrades to "no engine" is exactly the hole being closed.
     */
    private fun provision(): File {
        val ref = pinnedRef() ?: throw AssertionError("version.properties carries no SINGBOX_REF")
        val root = repoRoot() ?: throw AssertionError("cannot locate the repository root from " + File("").absolutePath)
        val target = File(root, ".sbx")
        val binary = File(target, "sing-box")
        val version = ref.removePrefix("v")
        val url = "https://github.com/SagerNet/sing-box/releases/download/" + ref +
            "/sing-box-" + version + "-linux-amd64.tar.gz"
        val tarball = File.createTempFile("sing-box-", ".tar.gz")
        try {
            val downloads = exec("curl", "-fsSL", "--retry", "3", "--retry-all-errors", "-o", tarball.absolutePath, url)
            if (downloads.first != 0) {
                throw AssertionError("cannot download the pinned engine " + ref + ": " + downloads.second)
            }
            target.mkdirs()
            val unpacked = exec("tar", "xzf", tarball.absolutePath, "-C", target.absolutePath, "--strip-components=1")
            if (unpacked.first != 0) {
                throw AssertionError("cannot unpack the pinned engine " + ref + ": " + unpacked.second)
            }
            binary.setExecutable(true)
            if (!binary.isFile) throw AssertionError("the pinned engine is missing after unpack: " + binary.path)
            return binary
        } finally {
            tarball.delete()
        }
    }

    private fun exec(vararg command: String): Pair<Int, String> {
        val process = ProcessBuilder(*command).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().use { it.readText() }
        assertTrue("'" + command.first() + "' did not finish in 300 s", process.waitFor(300, TimeUnit.SECONDS))
        return process.exitValue() to output
    }

    private fun repoRoot(): File? {
        var directory = File("").absoluteFile
        repeat(4) {
            if (File(directory, "version.properties").isFile && File(directory, "settings.gradle.kts").isFile) {
                return directory
            }
            directory = directory.parentFile ?: return null
        }
        return null
    }

    private fun pinnedRef(): String? {
        val properties = repoRoot()?.resolve("version.properties") ?: return null
        if (!properties.isFile) return null
        return properties.readLines()
            .firstOrNull { it.trimStart().startsWith("SINGBOX_REF") }
            ?.substringAfter('=')
            ?.trim()
            ?.takeIf { it.isNotEmpty() && !it.contains(' ') }
    }

    /** The per-family key is assembled at runtime: the source tree must not spell it out. */
    private fun withRemovedFamilyKey(base: String): String {
        val root = Json.parseToJsonElement(base) as JsonObject
        val inbounds = (root["inbounds"] as JsonArray).map { element ->
            val inbound = element as JsonObject
            val tun = (inbound["type"] as? JsonPrimitive)?.content == "tun"
            if (tun) JsonObject(inbound + (REMOVED_KEY to JsonPrimitive("fd00:1::1/64"))) else inbound
        }
        return JsonObject(root + ("inbounds" to JsonArray(inbounds))).toString()
    }

    /**
     * The panel profile's shape, reduced to what the chain under test touches: one tun inbound
     * without IPv6, a selector/urltest pair and a route rule aimed at "select" (the rule the CDN
     * variant has to repoint). No protocol outbound, so nothing needs a server or a handshake.
     */
    private val fixture = """
        {
          "log": {"level": "warn"},
          "inbounds": [
            {"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30"],
             "auto_route": true, "stack": "system", "strict_route": true}
          ],
          "outbounds": [
            {"type": "direct", "tag": "direct"},
            {"type": "direct", "tag": "node-a"},
            {"type": "urltest", "tag": "auto", "outbounds": ["node-a", "direct"],
             "url": "https://www.gstatic.com/generate_204"},
            {"type": "selector", "tag": "select", "outbounds": ["auto", "node-a", "direct"], "default": "auto"}
          ],
          "route": {
            "final": "select",
            "rules": [
              {"action": "route", "domain_suffix": ["example.com"], "outbound": "select"},
              {"action": "route", "ip_is_private": true, "outbound": "direct"}
            ]
          }
        }
    """.trimIndent()

    private val cdnRoute = WhiteListRuntimeRoute(
        routeId = "r1",
        label = "gate",
        transportProfileId = "profile",
        transportReleaseId = "release",
        compatibilityPresetId = "preset",
        address = "203.0.113.7",
        port = 443,
        serverName = "edge.example",
        host = "edge.example",
        path = "/x",
        clientId = "client",
        encryption = "aes-128-gcm",
    )

    private companion object {
        const val SOCKS_PORT = 10_860
        const val CDN_USER = "gateuser0123456789"
        const val CDN_PASS = "gatepass9876543210"
        const val LEGACY_ERROR =
            "legacy tun address fields are deprecated in sing-box 1.10.0 and removed in sing-box 1.12.0"
        val REMOVED_KEY = "inet6" + "_" + "address"
    }
}

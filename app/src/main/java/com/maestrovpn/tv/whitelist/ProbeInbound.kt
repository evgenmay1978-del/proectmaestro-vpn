package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.net.InetAddress
import java.net.ServerSocket
import java.security.SecureRandom

/**
 * A loopback SOCKS5 entry added to the config the app is about to hand to the engine.
 *
 * Why it exists (audit 28.10.2026, D3): "Connected" was posted as soon as the engine accepted the
 * config, and nothing ever verified that a single byte could cross the tunnel. A plain HTTPS request
 * from this app cannot verify it either: PackagePolicy excludes our own package in every branch, so
 * the app's own sockets always bypass the tun. The only honest test is a request that enters the
 * engine and leaves it through the configured routing/outbounds — i.e. a locally bound inbound.
 *
 * Binding a local proxy without authentication would hand the tunnel to any app on the device, so
 * the entry is created with random credentials per configuration apply. Listen is 127.0.0.1 only.
 *
 * This runs on every apply (ordinary and CDN): it is pure JSON, and the caller validates the result
 * with Libbox.checkConfig before using it and falls back to the unmodified config when the engine
 * refuses it. A probe channel that cannot be created must never cost a working tunnel.
 */
internal object ProbeInbound {
    const val TAG = "maestro-probe"
    private const val HEX = "0123456789abcdef"
    private const val MIN_PORT = 1024
    private const val MAX_PORT = 65_535
    private const val CREDENTIAL_BYTES = 24

    internal data class Endpoint(val port: Int, val username: String, val password: String) {
        /** Never let a credential reach a log line through println of a data class. */
        override fun toString(): String = "Endpoint(port=" + port + ")"
    }

    internal data class Attached(val config: String, val endpoint: Endpoint)

    /** Returns null when the config cannot carry the probe entry; the caller keeps the original. */
    fun attach(base: String): Attached? {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return null
        // Only touch a shape we recognise: a config without an inbounds array is not something the
        // app produces, and guessing what to do with it is exactly how a diagnostic breaks a tunnel.
        val inbounds = (root["inbounds"] as? JsonArray)?.toList() ?: return null
        val tags = inbounds.mapNotNull { ((it as? JsonObject)?.get("tag") as? JsonPrimitive)?.content }
        if (TAG in tags) return null
        val port = freePort() ?: return null
        val endpoint = Endpoint(port, credential(), credential())
        val users = JsonArray(listOf(JsonObject(mapOf(
            "username" to JsonPrimitive(endpoint.username),
            "password" to JsonPrimitive(endpoint.password),
        ))))
        val inbound = buildJsonObject {
            put("type", "socks")
            put("tag", TAG)
            put("listen", "127.0.0.1")
            put("listen_port", port)
            put("users", users)
        }
        val updated = JsonObject(root + ("inbounds" to JsonArray(inbounds + inbound)))
        return Attached(updated.toString(), endpoint)
    }

    /**
     * Picks a port that is free right now. The window between this check and the engine's bind is a
     * few milliseconds; a lost race surfaces as a refused probe, never as a broken config.
     */
    private fun freePort(): Int? = runCatching {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
    }.getOrNull()?.takeIf { it in MIN_PORT..MAX_PORT }

    /** Hex on purpose: valid SOCKS5 user/pass bytes on every API level and testable off-device. */
    private fun credential(): String {
        val bytes = ByteArray(CREDENTIAL_BYTES)
        SecureRandom().nextBytes(bytes)
        val text = StringBuilder(bytes.size * 2)
        for (byte in bytes) {
            val value = byte.toInt() and 0xFF
            text.append(HEX[value ushr 4]).append(HEX[value and 0x0F])
        }
        return text.toString()
    }
}

package com.maestrovpn.tv.aivpn

import android.util.Base64
import org.json.JSONObject

/** Raised for any malformed AIVPN connection key; the message is what the settings screen shows. */
class AivpnKeyException(message: String) : Exception(message)

/**
 * One parsed AIVPN connection key:
 *
 *     aivpn://<base64url({"s":"host:port","k":"<32B server pubkey>","p":"<32B psk>",...})>
 *
 * Mirrors the reference client's parser (aivpn v1.2.0, ConnectionKeyParser.kt): both the current
 * nested form (n.client_ip / n.server_vpn_ip / n.mtu / n.prefix_len) and the legacy flat "i"
 * field are accepted, because keys already handed out by the server carry either shape.
 */
data class AivpnKey(
    val serverHost: String,
    val serverPort: Int,
    val serverKey: ByteArray,
    val psk: ByteArray?,
    val serverSigningKey: ByteArray?,
    val maskOperatorPubkey: ByteArray?,
    val vpnIp: String,
    val serverVpnIp: String,
    val prefixLen: Int,
    val mtu: Int,
    val keepaliveSecs: Int,
) {
    /** "host:port", safe to show in the UI (no secret material). */
    val serverAddress: String
        get() = "$serverHost:$serverPort"

    companion object {
        const val SCHEME = "aivpn://"
        const val DEFAULT_MTU = 1280
        const val DEFAULT_PREFIX_LEN = 24
        const val DEFAULT_SERVER_VPN_IP = "10.0.0.1"

        private const val KEY_BYTES = 32
        private const val MIN_MTU = 576
        private const val MAX_MTU = 1500
        private const val MAX_PREFIX_LEN = 30

        fun parse(text: String): AivpnKey {
            val trimmed = text.trim()
            if (trimmed.isEmpty()) throw AivpnKeyException("key is empty")
            val payload = trimmed.removePrefix(SCHEME)

            val json = decodeJson(payload)
            val server = json.optText("s") ?: throw AivpnKeyException("field \"s\" is missing")
            val (host, port) = splitServer(server)

            val serverKey = requiredKey(json, "k")
            val psk = optionalKey(json, "p")
            val serverSigningKey = optionalKey(json, "sk")
            val maskOperatorPubkey = optionalKey(json, "mop")

            val network = json.optJSONObject("n")
            val vpnIp = network?.optText("client_ip") ?: json.optText("i")
                ?: throw AivpnKeyException("vpn address is missing (n.client_ip or i)")
            if (!isValidIpv4(vpnIp)) throw AivpnKeyException("vpn address \"$vpnIp\" is not a usable IPv4 host")

            val serverVpnIp = network?.optText("server_vpn_ip") ?: DEFAULT_SERVER_VPN_IP
            if (!isValidIpv4(serverVpnIp)) {
                throw AivpnKeyException("server vpn address \"$serverVpnIp\" is not a usable IPv4 host")
            }

            val prefixLen = network?.optInt("prefix_len", DEFAULT_PREFIX_LEN) ?: DEFAULT_PREFIX_LEN
            if (prefixLen !in 1..MAX_PREFIX_LEN) {
                throw AivpnKeyException("prefix_len must be 1..$MAX_PREFIX_LEN, got $prefixLen")
            }
            // VpnService.Builder.establish() rejects a subnet address or broadcast address with an
            // opaque "Cannot set address"; refuse it here so a bad key never reaches the builder.
            if (!isAssignableHost(vpnIp, prefixLen)) {
                throw AivpnKeyException("vpn address \"$vpnIp\" is not assignable inside /$prefixLen")
            }

            val mtu = network?.optInt("mtu", DEFAULT_MTU) ?: DEFAULT_MTU
            if (mtu !in MIN_MTU..MAX_MTU) {
                throw AivpnKeyException("mtu must be $MIN_MTU..$MAX_MTU, got $mtu")
            }
            val keepaliveSecs = network?.optInt("keepalive_secs", 0) ?: 0

            return AivpnKey(
                serverHost = host,
                serverPort = port,
                serverKey = serverKey,
                psk = psk,
                serverSigningKey = serverSigningKey,
                maskOperatorPubkey = maskOperatorPubkey,
                vpnIp = vpnIp,
                serverVpnIp = serverVpnIp,
                prefixLen = prefixLen,
                mtu = mtu,
                keepaliveSecs = keepaliveSecs,
            )
        }

        private fun decodeJson(payload: String): JSONObject {
            val bytes = try {
                base64(payload)
            } catch (_: IllegalArgumentException) {
                throw AivpnKeyException("payload is not base64url")
            }
            val text = String(bytes, Charsets.UTF_8)
            return try {
                JSONObject(text)
            } catch (_: Exception) {
                throw AivpnKeyException("payload is not a JSON object")
            }
        }

        /** Keys in the wild are base64url; the plain alphabet is accepted as well. */
        private fun base64(value: String): ByteArray =
            try {
                Base64.decode(value, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)
            } catch (_: IllegalArgumentException) {
                Base64.decode(value, Base64.DEFAULT)
            }

        private fun requiredKey(json: JSONObject, field: String): ByteArray =
            optionalKey(json, field) ?: throw AivpnKeyException("field \"$field\" is missing")

        private fun optionalKey(json: JSONObject, field: String): ByteArray? {
            val value = json.optText(field) ?: return null
            val bytes = try {
                base64(value)
            } catch (_: IllegalArgumentException) {
                throw AivpnKeyException("field \"$field\" is not base64")
            }
            if (bytes.size != KEY_BYTES) {
                throw AivpnKeyException("field \"$field\" must be $KEY_BYTES bytes, got ${bytes.size}")
            }
            return bytes
        }

        private fun splitServer(value: String): Pair<String, Int> {
            if (value.startsWith("[")) { // [::1]:443
                val end = value.indexOf(']')
                if (end <= 1) throw AivpnKeyException("field \"s\" is malformed: $value")
                val port = value.substring(end + 1).removePrefix(":").toIntOrNull()
                    ?: throw AivpnKeyException("field \"s\" has no port: $value")
                return value.substring(1, end) to port.also { checkPort(it) }
            }
            val separator = value.lastIndexOf(':')
            if (separator <= 0) throw AivpnKeyException("field \"s\" must be host:port, got $value")
            val host = value.substring(0, separator)
            val port = value.substring(separator + 1).toIntOrNull()
                ?: throw AivpnKeyException("field \"s\" has a non-numeric port: $value")
            checkPort(port)
            return host to port
        }

        private fun checkPort(port: Int) {
            if (port !in 1..65535) throw AivpnKeyException("port must be 1..65535, got $port")
        }

        private fun JSONObject.optText(field: String): String? {
            if (!has(field) || isNull(field)) return null
            return optString(field).trim().ifEmpty { null }
        }

        private fun isValidIpv4(value: String): Boolean {
            val octets = octetsOf(value) ?: return false
            val first = octets[0]
            // 0.x = unspecified, 127.x = loopback, 224-239 = multicast, >=240 = reserved/broadcast.
            return !(first == 0 || first == 127 || first in 224..239 || first >= 240)
        }

        private fun octetsOf(value: String): IntArray? {
            val parts = value.split(".")
            if (parts.size != 4) return null
            val out = IntArray(4)
            for (i in 0 until 4) {
                val n = parts[i].toIntOrNull() ?: return null
                if (n !in 0..255) return null
                out[i] = n
            }
            return out
        }

        /** False for the network / broadcast address of the /[prefixLen] subnet. */
        private fun isAssignableHost(ip: String, prefixLen: Int): Boolean {
            val octets = octetsOf(ip) ?: return false
            if (prefixLen !in 1..30) return true
            val address =
                (octets[0] shl 24) or (octets[1] shl 16) or (octets[2] shl 8) or octets[3]
            val hostBits = 32 - prefixLen
            val hostMask = (1L shl hostBits) - 1
            val host = address.toLong() and hostMask
            return host != 0L && host != hostMask
        }
    }
}

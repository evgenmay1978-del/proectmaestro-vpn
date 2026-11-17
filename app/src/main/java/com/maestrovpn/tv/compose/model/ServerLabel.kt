package com.maestrovpn.tv.compose.model

import com.maestrovpn.tv.compose.premium.flagCountryCode
import com.maestrovpn.tv.compose.premium.flagEmojiOf
import com.maestrovpn.tv.compose.premium.stripServerDecorations
import java.util.Locale

/**
 * How a server is shown on every screen — the phone list, the JFL picker, the TV tiles and the TV
 * server list: [flag] goes to the round badge, [name] is the country/server name without provider
 * prefixes and panel pictographs, [detail] names the transport. Where a server comes from (JFL, WL,
 * the xhttp runner) is not printed: the owner removed the «внешний сервер» caption (15.11.2026).
 */
internal data class ServerLabel(val flag: String, val name: String, val detail: String)

/** Provider and service prefixes the panel and the S1 shim put in front of a server name. */
private val SERVICE_PREFIXES = listOf("БС/Yandex ", "JFL · ", "WL · ")

/** The last « · » part of a tag that names a transport rather than the server. */
private val TRANSPORT_SUFFIX = Regex(
    "(?:VLESS|VMess|Trojan|Shadowsocks|Hysteria2?|TUIC|WireGuard|XHTTP|S\\d)" +
        "(?:\\s+.*)?",
    RegexOption.IGNORE_CASE,
)

/** Our raw panel tags («🇪🇸 MaestroVPN VLESS») carry the country only as the flag. */
private val COUNTRY_NAMES = mapOf(
    "ES" to "Испания",
    "NL" to "Нидерланды",
    "IT" to "Италия",
    "CZ" to "Чехия",
    "DE" to "Германия",
)

/** Transport of a libbox outbound type; null for groups and service outbounds. */
internal fun serverTransport(type: String?, subXhttp: Boolean = false): String? =
    when (type?.trim()?.lowercase(Locale.ROOT)) {
        "vless" -> "VLESS"
        "vmess" -> "VMess"
        "trojan" -> "Trojan"
        "shadowsocks" -> "Shadowsocks"
        "hysteria2" -> "Hysteria2"
        "hysteria" -> "Hysteria"
        "tuic" -> "TUIC"
        "wireguard" -> "WireGuard"
        // xhttp nodes reach the engine as a loopback SOCKS to the Xray runner.
        else -> if (subXhttp) "VLESS · XHTTP" else null
    }

/**
 * Label of a server tag that names a server (it carries a country flag or a provider prefix);
 * null for service keys («auto», «vless», CDN, AWG), which every screen maps on its own.
 *
 * @param type libbox outbound type of the tag when known (the groups feed and the saved profile).
 * @param subXhttp the tag is an xhttp node of the subscription (SubXhttpRuntime.isSubXhttpTag).
 */
internal fun serverLabel(tag: String?, type: String? = null, subXhttp: Boolean = false): ServerLabel? {
    var text = tag?.trim().orEmpty()
    if (text.isEmpty() || text.startsWith("cdn:")) return null
    var prefixed = false
    var stripped = true
    while (stripped) {
        stripped = false
        for (prefix in SERVICE_PREFIXES) {
            if (text.startsWith(prefix)) {
                text = text.removePrefix(prefix).trim()
                prefixed = true
                stripped = true
            }
        }
    }
    val flag = flagEmojiOf(text)
    if (flag.isEmpty() && !prefixed && !subXhttp) return null
    val clean = stripServerDecorations(text)
    val parts = clean.split(" · ").map { it.trim() }.filter { it.isNotEmpty() }
    var name: String
    var tagTransport: String? = null
    when {
        "maestrovpn" in clean.lowercase(Locale.ROOT) -> {
            name = COUNTRY_NAMES[flagCountryCode(flag)] ?: "MaestroVPN"
            tagTransport = clean.replace("MaestroVPN", " ", ignoreCase = true)
                .split(' ').filter { it.isNotBlank() }.joinToString(" ").ifBlank { null }
        }
        // «🇫🇮 Akonit · Финляндия»: the first part is the provider brand, the rest is the place.
        subXhttp && parts.size >= 2 -> name = parts.drop(1).joinToString(" · ")
        parts.size >= 2 && TRANSPORT_SUFFIX.matches(parts.last()) -> {
            name = parts.dropLast(1).joinToString(" · ")
            tagTransport = parts.last()
        }
        else -> name = parts.joinToString(" · ")
    }
    if (name.isEmpty()) name = clean.ifEmpty { text }
    val detail = serverTransport(type, subXhttp) ?: tagTransport ?: "VPN"
    return ServerLabel(flag = flag, name = name, detail = detail)
}

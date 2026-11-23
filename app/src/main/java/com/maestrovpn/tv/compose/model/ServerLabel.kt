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
    "LU" to "Люксембург",
    "RU" to "Россия",
    "AT" to "Австрия",
    "EE" to "Эстония",
    "DE" to "Германия",
    "LV" to "Латвия",
    "LT" to "Литва",
    "PL" to "Польша",
    "SK" to "Словакия",
    "SI" to "Словения",
    "HU" to "Венгрия",
    "BG" to "Болгария",
    "UA" to "Украина",
    "BY" to "Беларусь",
    "RS" to "Сербия",
    "HR" to "Хорватия",
    "BA" to "Босния и Герцеговина",
    "IT" to "Италия",
    "FR" to "Франция",
    "BE" to "Бельгия",
    "RO" to "Румыния",
    "MD" to "Молдова",
    "IE" to "Ирландия",
    "AD" to "Андорра",
    "MT" to "Мальта",
    "VA" to "Ватикан",
    "SM" to "Сан-Марино",
    "MC" to "Монако",
    "LI" to "Лихтенштейн",
    "PT" to "Португалия",
    "GR" to "Греция",
    "CH" to "Швейцария",
    "CZ" to "Чехия",
    "SE" to "Швеция",
    "FI" to "Финляндия",
    "DK" to "Дания",
    "NO" to "Норвегия",
    "IS" to "Исландия",
    "FO" to "Фарерские острова",
    "GB" to "Великобритания",
    "GE" to "Грузия",
    "AM" to "Армения",
    "AZ" to "Азербайджан",
    "TR" to "Турция",
    "MK" to "Северная Македония",
    "EU" to "Европа",
    "KZ" to "Казахстан",
    "UZ" to "Узбекистан",
    "KG" to "Киргизия",
    "TJ" to "Таджикистан",
    "MN" to "Монголия",
    "CN" to "Китай",
    "HK" to "Гонконг",
    "TW" to "Тайвань",
    "JP" to "Япония",
    "KR" to "Южная Корея",
    "VN" to "Вьетнам",
    "TH" to "Таиланд",
    "LA" to "Лаос",
    "MM" to "Мьянма",
    "MY" to "Малайзия",
    "SG" to "Сингапур",
    "ID" to "Индонезия",
    "PH" to "Филиппины",
    "IN" to "Индия",
    "PK" to "Пакистан",
    "BD" to "Бангладеш",
    "IL" to "Израиль",
    "AE" to "ОАЭ",
    "KW" to "Кувейт",
    "QA" to "Катар",
    "BH" to "Бахрейн",
    "JO" to "Иордания",
    "PS" to "Палестина",
    "LB" to "Ливан",
    "IQ" to "Ирак",
    "SY" to "Сирия",
    "YE" to "Йемен",
    "EG" to "Египет",
    "IR" to "Иран",
    "NG" to "Нигерия",
    "GH" to "Гана",
    "MA" to "Марокко",
    "DZ" to "Алжир",
    "TN" to "Тунис",
    "US" to "США",
    "CA" to "Канада",
    "MX" to "Мексика",
    "BR" to "Бразилия",
    "AR" to "Аргентина",
    "CL" to "Чили",
    "CO" to "Колумбия",
    "EC" to "Эквадор",
    "VE" to "Венесуэла",
    "PE" to "Перу",
    "BO" to "Боливия",
    "PY" to "Парагвай",
    "UY" to "Уругвай",
    "CR" to "Коста-Рика",
    "CU" to "Куба",
    "AU" to "Австралия",
    "NZ" to "Новая Зеландия",
)

/** Russian country name for an emoji flag ("🇩🇪" → "Германия"), null when unknown. */
internal fun countryNameForFlag(flag: String): String? = COUNTRY_NAMES[flagCountryCode(flag)]

/**
 * Display name for a cleaned label (flag already stripped by [stripServerDecorations]) whose
 * flag is known: the country from the flag, with a provider brand prefix dropped
 * ("Яук – Германия" → "Германия", "Раю - Эстония" → "Эстония") and a trailing
 * non-transport tag kept ("Швеция · Torrent"). Null when the flag carries no known country.
 */
internal fun displayNameFromFlag(clean: String, flag: String): String? {
    val country = countryNameForFlag(flag) ?: return null
    val segments = clean.split(" · ", " – ", " - ").map { it.trim() }.filter { it.isNotEmpty() }
    val countryIdx = segments.indexOf(country)
    if (countryIdx < 0) return country
    val after = segments.drop(countryIdx + 1).filter { !TRANSPORT_SUFFIX.matches(it) }
    return if (after.isEmpty()) country else "$country · ${after.joinToString(" · ")}"
}

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
        // «🇩🇪 Яук – Германия»: the panel prefixes the country with a provider brand
        // ("Яук", "Раю", "Вьюга") joined by " – " / " - " / " · ". The flag is the country:
        // its name wins and the brand is dropped (a trailing non-transport tag is kept).
        flag.isNotEmpty() -> {
            val resolved = displayNameFromFlag(clean, flag)
            if (resolved != null) {
                name = resolved
                tagTransport = clean.split(" · ", " – ", " - ").map { it.trim() }
                    .firstOrNull { TRANSPORT_SUFFIX.matches(it) }
            } else {
                name = parts.joinToString(" · ")
            }
        }
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

package com.maestrovpn.tv.bg

import java.net.URI

internal enum class WdttStage {
    STARTING,
    DNS,
    TLS,
    VK_AUTH,
    CAPTCHA_REQUIRED,
    TURN_ALLOCATED,
    DTLS,
    WRAP,
    WIREGUARD,
    READY,
    FAILED,
    STOPPED,
}

internal enum class WdttSafeErrorCode {
    OK,
    INPUT_INVALID,
    TLS_TRUST_FAILED,
    VK_AUTH_FAILED,
    CAPTCHA_REQUIRED,
    TURN_ALLOCATE_FAILED,
    DTLS_FAILED,
    WRAP_FAILED,
    WIREGUARD_FAILED,
    PROVIDER_UNAVAILABLE,
    INTERNAL,
}

internal data class WdttCaptchaRequest(
    val mode: String,
    val redirectUri: String,
    val sessionToken: String,
) {
    override fun toString(): String =
        "WdttCaptchaRequest(mode=$mode, redirectUri=<redacted>, sessionToken=<redacted>)"
}

internal sealed class WdttEvent {
    data class Stage(val stage: WdttStage) : WdttEvent()
    data class Failure(val code: WdttSafeErrorCode, val fatal: Boolean) : WdttEvent()
    data class Captcha(val request: WdttCaptchaRequest) : WdttEvent()
}

private val wdttStageEvent = Regex(
    """__WDTT_EVENT__\|STAGE\|\{"stage":"([A-Z_]+)"\}""",
)
private val wdttErrorEvent = Regex(
    """__WDTT_EVENT__\|ERROR\|\{"code":"([^"\\]{1,128})","fatal":(true|false)\}""",
)
private val wdttCaptchaModes = setOf("auto", "manual", "selected")

internal fun parseWdttEvent(line: String): WdttEvent? {
    wdttStageEvent.matchEntire(line)?.let { match ->
        val stage = WdttStage.entries.firstOrNull { it.name == match.groupValues[1] } ?: return null
        return WdttEvent.Stage(stage)
    }

    wdttErrorEvent.matchEntire(line)?.let { match ->
        val code = WdttSafeErrorCode.entries.firstOrNull { it.name == match.groupValues[1] }
            ?: WdttSafeErrorCode.INTERNAL
        return WdttEvent.Failure(code, fatal = match.groupValues[2] == "true")
    }

    if (!line.startsWith("CAPTCHA_SOLVE|")) return null
    val fields = line.split('|', limit = 4)
    if (fields.size != 4 || fields[0] != "CAPTCHA_SOLVE") return null
    val mode = fields[1]
    val redirect = fields[2]
    val session = fields[3]
    if (mode !in wdttCaptchaModes || session.isBlank() || session.length > 4_096) return null
    if (session.any(Char::isISOControl) || redirect.length !in 1..2_048 || redirect.any(Char::isISOControl)) return null
    val uri = runCatching { URI(redirect) }.getOrNull() ?: return null
    if (!uri.scheme.equals("https", ignoreCase = true) || uri.host.isNullOrBlank() || uri.userInfo != null) return null

    return WdttEvent.Captcha(WdttCaptchaRequest(mode, redirect, session))
}

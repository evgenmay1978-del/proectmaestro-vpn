package com.maestrovpn.tv.compose.wdtt

import java.net.URI

internal sealed class WdttCaptchaResult {
    data class Success(val token: String) : WdttCaptchaResult()
    data object Cancelled : WdttCaptchaResult()
    data object Timeout : WdttCaptchaResult()
}

internal object WdttCaptchaPolicy {
    private val allowedHosts = setOf(
        "id.vk.com",
        "oauth.vk.com",
        "login.vk.com",
        "vk.com",
        "m.vk.com",
        "id.vk.ru",
    )

    fun isAllowedTopLevel(value: String): Boolean {
        if (value.isBlank() || value.length > 2_048 || value.any(Char::isISOControl) || '\\' in value) {
            return false
        }
        val uri = runCatching { URI(value) }.getOrNull() ?: return false
        val host = uri.host?.lowercase() ?: return false
        if (!uri.scheme.equals("https", ignoreCase = true)) return false
        if (uri.userInfo != null || uri.port != -1 || uri.fragment != null) return false
        if (!uri.rawAuthority.equals(uri.host, ignoreCase = true)) return false
        return host in allowedHosts
    }

    fun sanitizeSuccessToken(value: String): String? {
        if (value.length !in 1..4_096 || value.isBlank() || value != value.trim()) return null
        if (value.any(Char::isISOControl)) return null
        return value
    }
}

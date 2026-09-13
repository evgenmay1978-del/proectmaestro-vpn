package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.bg.UpdateProfileWork
import java.net.URI

/** The existing bot start command binds the selected phone account before purchase. */
internal fun phoneCdnPurchaseLink(
    subscriptionUrl: String?, capturedAccount: Pair<Long, Long>, currentAccount: Pair<Long, Long>,
): String? {
    if (capturedAccount.first < 0 || capturedAccount != currentAccount ||
        !UpdateProfileWork.isTrustedSubUrl(subscriptionUrl)) return null
    val source = runCatching { URI(subscriptionUrl) }.getOrNull() ?: return null
    if (source.rawUserInfo != null || source.rawFragment != null) return null
    // Telegram permits at most 64 URL-safe start characters, including "maestro_".
    val token = Regex("^/sub/([A-Za-z0-9_-]{1,56})$").matchEntire(source.rawPath.orEmpty())
        ?.groupValues?.get(1) ?: return null
    return "https://t.me/MaestroSecureVPN_bot?start=maestro_$token"
}

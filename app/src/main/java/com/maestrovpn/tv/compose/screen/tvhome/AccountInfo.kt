package com.maestrovpn.tv.compose.screen.tvhome

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.bg.WdttManager
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.MaestroSub
import com.maestrovpn.tv.utils.httpGetStringTimed
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.security.MessageDigest

/** The active subscription's login + days remaining + expiry date, for the home screen. Null
 *  fields mean "unknown" (no subscription profile yet, or the panel was unreachable). */
data class AccountInfo(
    val login: String? = null,
    val daysLeft: Int? = null,
    val hasSubProfile: Boolean = false,
    /** дата окончания подписки «ДД.ММ.ГГГГ» из /info `expires` (RFC3339) — для строки аккаунта */
    val expiresDate: String? = null,
    /** Non-secret hash binding cached owner state to the exact trusted local subscription. */
    val subscriptionIdentity: String? = null,
)

internal fun accountInfoAfterRefresh(
    previous: AccountInfo,
    refreshed: AccountInfo,
    allowPreviousIdentityFallback: Boolean,
): AccountInfo =
    when {
        !refreshed.hasSubProfile -> AccountInfo()
        refreshed.login != null -> refreshed
        allowPreviousIdentityFallback &&
            previous.login != null &&
            previous.subscriptionIdentity != null &&
            previous.subscriptionIdentity == refreshed.subscriptionIdentity -> previous
        else -> refreshed
    }

private const val ACCOUNT_CACHE_PREFS = "maestro_account_info"
private const val ACCOUNT_CACHE_IDENTITY = "subscription_identity"
private const val ACCOUNT_CACHE_LOGIN = "login"
private const val ACCOUNT_CACHE_DAYS = "days_left"
private const val ACCOUNT_CACHE_EXPIRES = "expires_date"

private fun subscriptionIdentity(profileId: Long, remoteUrl: String): String? {
    val token = MaestroSub.token(remoteUrl).takeIf { it.isNotBlank() } ?: return null
    val digest = MessageDigest.getInstance("SHA-256")
        .digest("maestro-sub:$token".toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it.toInt() and 0xFF) }
    return "$profileId:${digest.take(32)}"
}

private fun cachedAccountInfo(identity: String, hasSubProfile: Boolean): AccountInfo? =
    runCatching {
        val prefs = Application.application.getSharedPreferences(ACCOUNT_CACHE_PREFS, Context.MODE_PRIVATE)
        if (prefs.getString(ACCOUNT_CACHE_IDENTITY, null) != identity) return@runCatching null
        val login = prefs.getString(ACCOUNT_CACHE_LOGIN, null)?.takeIf { it.isNotBlank() }
            ?: return@runCatching null
        AccountInfo(
            login = login,
            daysLeft = if (prefs.contains(ACCOUNT_CACHE_DAYS)) prefs.getInt(ACCOUNT_CACHE_DAYS, 0) else null,
            hasSubProfile = hasSubProfile,
            expiresDate = prefs.getString(ACCOUNT_CACHE_EXPIRES, null)?.takeIf { it.isNotBlank() },
            subscriptionIdentity = identity,
        )
    }.getOrNull()

private fun cacheAccountInfo(info: AccountInfo) {
    val identity = info.subscriptionIdentity ?: return
    val login = info.login ?: return
    runCatching {
        val editor = Application.application
            .getSharedPreferences(ACCOUNT_CACHE_PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(ACCOUNT_CACHE_IDENTITY, identity)
            .putString(ACCOUNT_CACHE_LOGIN, login)
        if (info.daysLeft == null) editor.remove(ACCOUNT_CACHE_DAYS)
        else editor.putInt(ACCOUNT_CACHE_DAYS, info.daysLeft)
        if (info.expiresDate == null) editor.remove(ACCOUNT_CACHE_EXPIRES)
        else editor.putString(ACCOUNT_CACHE_EXPIRES, info.expiresDate)
        editor.apply()
    }
}

private fun clearAccountInfoCache() {
    runCatching {
        Application.application
            .getSharedPreferences(ACCOUNT_CACHE_PREFS, Context.MODE_PRIVATE)
            .edit()
            .clear()
            .apply()
    }
}

internal fun shouldClearPrivateTransportCreds(isMobile: Boolean, credentialsMatch: Boolean): Boolean =
    isMobile && !credentialsMatch

private fun clearPrivateTransportCreds() {
    OlcrtcManager.setCreds(provider = null, room = null, key = null, transport = null)
    WdttManager.setCreds(
        peer = null,
        vkHashes = null,
        password = null,
        workers = null,
        fingerprint = null,
        clientIds = null,
        obfsMode = null,
    )
}

/** "2026-08-02T15:04:05Z"/"+03:00"-варианты → "02.08.2026"; мусор → null (строка просто короче). */
private fun formatExpires(raw: String?): String? {
    val date = raw?.substringBefore('T') ?: return null
    val p = date.split('-')
    if (p.size != 3 || p[0].length != 4) return null
    return "${p[2]}.${p[1]}.${p[0]}"
}

/**
 * Fetches [AccountInfo] from the panel `GET /sub/<token>/info` using the active
 * MaestroVPN profile's `…/sub/<token>` URL. Refetches whenever [refreshKey] changes
 * (e.g. on connect/disconnect). Fully crash-safe: the work runs off the main thread and
 * any error collapses to empty info — it never throws out of the produceState coroutine
 * (a throw there would crash the whole app — see the produceState gotcha).
 */
@Composable
fun rememberAccountInfo(
    refreshKey: Any?,
    preserveVerifiedIdentityOnOutage: Boolean,
): State<AccountInfo> =
    produceState(initialValue = AccountInfo(), refreshKey, preserveVerifiedIdentityOnOutage) {
        val previous = value
        var currentIdentity: String? = null
        var hasSubProfile = previous.hasSubProfile
        var httpFallbackUsed = false
        val refreshed = runCatching {
            withContext(Dispatchers.IO) {
                // hasSubProfile is true whenever a MaestroVPN sub profile exists locally — even if
                // the panel is unreachable below — so a transient timeout never makes a payer "look
                // keyless" (drives the Trial-CTA gating in TvHomeScreen).
                val profiles = ProfileManager.list()
                hasSubProfile = profiles.any { it.typed.remoteURL.contains("/sub/") }
                // Fetch /info from a TRUSTED origin only. This response is not just displayed —
                // it sets the olcRTC room/key and the WDTT peer/password below, and the request
                // carries this install's device id. Selecting the profile by "contains /sub/"
                // alone let any imported profile (a sing-box:// deep link only asks for a
                // confirmation) become the source of both. Same boundary as the silent updater.
                val profile = profiles.firstOrNull {
                    it.typed.remoteURL.contains("/sub/") &&
                        UpdateProfileWork.isTrustedSubUrl(it.typed.remoteURL)
                }
                if (profile == null) {
                    clearAccountInfoCache()
                    if (preserveVerifiedIdentityOnOutage) clearPrivateTransportCreds()
                    return@withContext AccountInfo(hasSubProfile = hasSubProfile)
                }
                currentIdentity = subscriptionIdentity(profile.id, profile.typed.remoteURL)
                val cached = currentIdentity?.takeIf { preserveVerifiedIdentityOnOutage }
                    ?.let { cachedAccountInfo(it, hasSubProfile) }
                val credentialsMatch =
                    currentIdentity != null &&
                        (cached != null || currentIdentity == previous.subscriptionIdentity)
                val url = MaestroSub.endpoint(profile.typed.remoteURL, "info")
                val json = httpGetStringTimed(url)
                if (json == null) {
                    httpFallbackUsed = true
                    if (!credentialsMatch) clearAccountInfoCache()
                    if (shouldClearPrivateTransportCreds(preserveVerifiedIdentityOnOutage, credentialsMatch)) {
                        clearPrivateTransportCreds()
                    }
                    return@withContext cached ?: AccountInfo(
                        hasSubProfile = hasSubProfile,
                        subscriptionIdentity = currentIdentity,
                    )
                }
                val o = JSONObject(json)
                // olcRTC WebRTC params (owner-gated server-side) ride in /info, not /sub. Push them
                // into the manager so the olcRTC selector item becomes startable; a response without
                // them clears any stale creds. Inert for the fleet (only the owner's /info has it).
                val olc = o.optJSONObject("olcrtc")
                OlcrtcManager.setCreds(
                    provider = olc?.optString("provider"),
                    room = olc?.optString("room"),
                    key = olc?.optString("key"),
                    transport = olc?.optString("transport"),
                )
                val wdtt = o.optJSONObject("vk_turn")
                WdttManager.setCreds(
                    peer = wdtt?.optString("server"),
                    vkHashes = wdtt?.optJSONArray("vk_hashes")?.let { a ->
                        (0 until a.length()).map { a.optString(it) }
                    },
                    password = wdtt?.optString("password"),
                    workers = wdtt?.takeIf { it.has("workers") }?.optInt("workers"),
                    fingerprint = wdtt?.optString("fingerprint"),
                    clientIds = wdtt?.optJSONArray("client_ids")?.let { a ->
                        (0 until a.length()).map { a.optString(it) }
                    },
                    obfsMode = wdtt?.optString("obfs_mode"),
                )
                val info = AccountInfo(
                    login = o.optString("login").ifBlank { null },
                    daysLeft = if (o.has("days_left")) o.getInt("days_left") else null,
                    hasSubProfile = hasSubProfile,
                    expiresDate = formatExpires(o.optString("expires").ifBlank { null }),
                    subscriptionIdentity = currentIdentity,
                )
                if (preserveVerifiedIdentityOnOutage && info.login != null) cacheAccountInfo(info)
                else clearAccountInfoCache()
                info
            }
        }.getOrElse {
            httpFallbackUsed = true
            val cached = currentIdentity?.takeIf { preserveVerifiedIdentityOnOutage }
                ?.let { cachedAccountInfo(it, hasSubProfile) }
            val credentialsMatch =
                currentIdentity != null &&
                    (cached != null || currentIdentity == previous.subscriptionIdentity)
            if (!credentialsMatch) clearAccountInfoCache()
            if (shouldClearPrivateTransportCreds(preserveVerifiedIdentityOnOutage, credentialsMatch)) {
                clearPrivateTransportCreds()
            }
            cached ?: AccountInfo(
                hasSubProfile = hasSubProfile,
                subscriptionIdentity = currentIdentity,
            )
        }
        val allowPreviousIdentityFallback =
            httpFallbackUsed &&
                currentIdentity != null &&
                currentIdentity == previous.subscriptionIdentity
        value = accountInfoAfterRefresh(previous, refreshed, allowPreviousIdentityFallback)
    }

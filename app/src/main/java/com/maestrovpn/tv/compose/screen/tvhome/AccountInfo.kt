package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.bg.WdttManager
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.MaestroSub
import com.maestrovpn.tv.utils.httpGetStringTimed
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** The active subscription's login + days remaining + expiry date, for the home screen. Null
 *  fields mean "unknown" (no subscription profile yet, or the panel was unreachable). */
data class AccountInfo(
    val login: String? = null,
    val daysLeft: Int? = null,
    val hasSubProfile: Boolean = false,
    /** дата окончания подписки «ДД.ММ.ГГГГ» из /info `expires` (RFC3339) — для строки аккаунта */
    val expiresDate: String? = null,
)

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
fun rememberAccountInfo(refreshKey: Any?): State<AccountInfo> =
    produceState(initialValue = AccountInfo(), refreshKey) {
        value = runCatching {
            withContext(Dispatchers.IO) {
                // hasSubProfile is true whenever a MaestroVPN sub profile exists locally — even if
                // the panel is unreachable below — so a transient timeout never makes a payer "look
                // keyless" (drives the Trial-CTA gating in TvHomeScreen).
                val hasSubProfile = ProfileManager.list().any { it.typed.remoteURL.contains("/sub/") }
                val profile = ProfileManager.list()
                    .firstOrNull { it.typed.remoteURL.contains("/sub/") }
                    ?: return@withContext AccountInfo(hasSubProfile = hasSubProfile)
                val url = MaestroSub.endpoint(profile.typed.remoteURL, "info")
                val json = httpGetStringTimed(url) ?: return@withContext AccountInfo(hasSubProfile = hasSubProfile)
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
                AccountInfo(
                    login = o.optString("login").ifBlank { null },
                    daysLeft = if (o.has("days_left")) o.getInt("days_left") else null,
                    hasSubProfile = hasSubProfile,
                    expiresDate = formatExpires(o.optString("expires").ifBlank { null }),
                )
            }
        }.getOrDefault(AccountInfo())
    }

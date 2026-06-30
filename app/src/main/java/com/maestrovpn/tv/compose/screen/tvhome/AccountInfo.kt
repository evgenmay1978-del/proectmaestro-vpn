package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.httpGetStringTimed
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** The active subscription's login + days remaining, for the home screen. Null fields
 *  mean "unknown" (no subscription profile yet, or the panel was unreachable). */
data class AccountInfo(val login: String? = null, val daysLeft: Int? = null, val hasSubProfile: Boolean = false)

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
                val url = profile.typed.remoteURL.trimEnd('/') + "/info"
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
                AccountInfo(
                    login = o.optString("login").ifBlank { null },
                    daysLeft = if (o.has("days_left")) o.getInt("days_left") else null,
                    hasSubProfile = hasSubProfile,
                )
            }
        }.getOrDefault(AccountInfo())
    }

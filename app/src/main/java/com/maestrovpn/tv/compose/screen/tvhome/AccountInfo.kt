package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.HTTPClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** The active subscription's login + days remaining, for the home screen. Null fields
 *  mean "unknown" (no subscription profile yet, or the panel was unreachable). */
data class AccountInfo(val login: String? = null, val daysLeft: Int? = null)

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
                val profile = ProfileManager.list()
                    .firstOrNull { it.typed.remoteURL.contains("/sub/") }
                    ?: return@withContext AccountInfo()
                val url = profile.typed.remoteURL.trimEnd('/') + "/info"
                val json = HTTPClient().use { it.getString(url) }
                val o = JSONObject(json)
                AccountInfo(
                    login = o.optString("login").ifBlank { null },
                    daysLeft = if (o.has("days_left")) o.getInt("days_left") else null,
                )
            }
        }.getOrDefault(AccountInfo())
    }

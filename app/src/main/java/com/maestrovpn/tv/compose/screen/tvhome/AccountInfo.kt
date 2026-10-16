package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.DeviceFormFactor
import com.maestrovpn.tv.utils.MaestroSub
import com.maestrovpn.tv.utils.httpGetStringTimed
import com.maestrovpn.tv.whitelist.WhiteListClientInfoParser
import com.maestrovpn.tv.whitelist.WhiteListDisplayModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
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
    val whiteList: WhiteListDisplayModel? = null,
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
fun rememberAccountInfo(refreshKey: Any?): State<AccountInfo> {
    val context = LocalContext.current
    val isTelevision = DeviceFormFactor.isTelevision(context)
    val phoneAccountSelection = if (isTelevision) null else rememberPhoneAccountKey()
    val phoneAccountKey = phoneAccountSelection?.value
    val lastPhoneHasSubProfile = remember { mutableStateOf(false) }
    val lastPhoneLogin = rememberSaveable(phoneAccountKey?.first) { mutableStateOf<String?>(null) }
    return key(phoneAccountKey) {
        produceState(
            initialValue = AccountInfo(login = lastPhoneLogin.value, hasSubProfile = !isTelevision && lastPhoneHasSubProfile.value),
            refreshKey,
            isTelevision,
        ) {
            value = try {
                withContext(Dispatchers.IO) {
                    // hasSubProfile is true whenever a MaestroVPN sub profile exists locally — even if
                    // the panel is unreachable below — so a transient timeout never makes a payer "look
                    // keyless" (drives the Trial-CTA gating in TvHomeScreen).
                    val hasSubProfile = ProfileManager.list().any { it.typed.remoteURL.contains("/sub/") }
                    if (!isTelevision) lastPhoneHasSubProfile.value = hasSubProfile
                    // Fetch /info from a TRUSTED origin only: the request carries this install's
                    // device id, and picking the profile by "contains /sub/" alone let any imported
                    // profile (a sing-box:// deep link only asks for a confirmation) become the
                    // source. Same boundary as the silent updater.
                    // hasSubProfile stays permissive on purpose: it only gates the Trial CTA, and a
                    // payer must never look keyless because of it.
                    val profile = ProfileManager.list()
                        .firstOrNull {
                            (isTelevision || it.id == phoneAccountKey?.first) &&
                                it.typed.remoteURL.contains("/sub/") &&
                                UpdateProfileWork.isTrustedSubUrl(it.typed.remoteURL)
                        }
                        ?: return@withContext AccountInfo(hasSubProfile = hasSubProfile)
                    val url = MaestroSub.endpoint(profile.typed.remoteURL, "info")
                    val json = httpGetStringTimed(url) ?: return@withContext AccountInfo(login = lastPhoneLogin.value, hasSubProfile = hasSubProfile)
                    val o = JSONObject(json)
                    if (!isTelevision) {
                        coroutineContext.ensureActive()
                        if (phoneAccountSelection?.value != phoneAccountKey ||
                            Settings.selectedProfile != profile.id ||
                            ProfileManager.get(profile.id)?.typed?.remoteURL != profile.typed.remoteURL
                        ) return@withContext AccountInfo(hasSubProfile = hasSubProfile)
                    }
                    val resolvedLogin = o.optString("login").ifBlank { null }
                    if (!isTelevision && resolvedLogin != null) lastPhoneLogin.value = resolvedLogin
                    AccountInfo(
                        login = resolvedLogin ?: lastPhoneLogin.value,
                        daysLeft = if (o.has("days_left")) o.getInt("days_left") else null,
                        hasSubProfile = hasSubProfile,
                        expiresDate = formatExpires(o.optString("expires").ifBlank { null }),
                        whiteList = WhiteListClientInfoParser.parseInfoResponse(
                            raw = json,
                            isTelevision = isTelevision,
                        ),
                    )
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                AccountInfo(login = lastPhoneLogin.value, hasSubProfile = !isTelevision && lastPhoneHasSubProfile.value)
            }
        }
    }
}

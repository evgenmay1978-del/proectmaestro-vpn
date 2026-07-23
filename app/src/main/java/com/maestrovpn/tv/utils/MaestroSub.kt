package com.maestrovpn.tv.utils

import android.content.Context
import android.media.MediaDrm
import android.os.Build
import android.provider.Settings
import java.util.UUID

/**
 * Per-install identity + helpers for the MaestroVPN subscription URL.
 *
 * The backend caps an account at a fixed number of devices (default 5) by counting the
 * distinct device ids that fetch its subscription, which covers ALL protocols at once (every
 * protocol's creds are delivered through /sub). The id is derived from ANDROID_ID so it
 * SURVIVES a reinstall / data-clear on the same physical device — re-activating after a reinstall
 * is therefore NOT counted as a new device against the cap (owner request: "the app must know this
 * device was already installed and not count it as new"). It is hashed (not the raw SSAID) for
 * privacy, and falls back to a random UUID only when no stable anchor exists. Every /sub poll and
 * the /claim call carries it via ?device=<id>.
 *
 * The owner admin logins (wapmix/wapmixx) are exempted server-side, so they are never capped.
 */
object MaestroSub {
    private const val PREFS = "maestro_device"
    private const val KEY = "device_id"

    /**
     * Stable per-DEVICE id. Persisted once; on FIRST generation it is derived from ANDROID_ID so a
     * reinstall (which clears prefs) re-derives the SAME id → the backend does not count it as a new
     * device. Existing installs keep whatever id they already stored (no disruption / no re-count on
     * update). If a customer already tripped the cap from past reinstalls, clear it once server-side:
     * `POST /admin/reset-devices {login}`; afterwards reinstalls reuse the one stable id.
     */
    fun deviceId(context: Context): String {
        val sp = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        sp.getString(KEY, null)?.let { return it }
        val id = stableDeviceId(context)
        // commit() (synchronous), not apply(): the id MUST be on disk before we return it, else a
        // crash right after could regenerate a different id next launch (a fallback-UUID device would
        // then count as two). One-time tiny write.
        @Suppress("ApplySharedPref")
        sp.edit().putString(KEY, id).commit()
        return id
    }

    /**
     * A device id that SURVIVES reinstall/data-clear on the same physical device. ANDROID_ID (SSAID)
     * is stable per app-signing-key + device and only resets on factory reset — exactly the "same
     * device" semantics the cap needs. Hashed so we never send the raw SSAID. Falls back to a random
     * UUID (old per-install behaviour) when SSAID is missing or the well-known buggy constant, so the
     * cap still functions on those devices.
     */
    private fun stableDeviceId(context: Context): String {
        val ssaid = try {
            Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
        } catch (_: Exception) { null }
        if (ssaid.isNullOrBlank() || ssaid.length < 8 || ssaid == "9774d56d682e549c") {
            return UUID.randomUUID().toString()
        }
        return "d-" + sha256Hex("maestro-dev:$ssaid").take(32)
    }

    private fun sha256Hex(s: String): String =
        java.security.MessageDigest.getInstance("SHA-256")
            .digest(s.toByteArray())
            .joinToString("") { "%02x".format(it.toInt() and 0xFF) }

    /**
     * Anti-abuse anchor for the in-app free trial — DISTINCT from [deviceId]. Unlike the
     * per-install UUID (which regenerates on reinstall and so can't stop trial farming), this
     * composite is built from reinstall-surviving hardware-ish ids so the backend can enforce
     * "one trial per device, ever":
     *   - ANDROID_ID (SSAID): survives app reinstall/clear-data (resets only on factory reset);
     *     present on every Android incl. no-GMS boxes — the primary anchor.
     *   - Widevine/MediaDrm device id: survives even a factory reset where DRM is provisioned;
     *     absent on cheap L3 boxes (UnsupportedSchemeException) → empty, then we lean on SSAID.
     *   - Build.MODEL: lowers MediaDrm/SSAID collisions across distinct units.
     * Sent raw in the POST /trial body; the SERVER salts + HMACs it (no secret salt in the APK).
     */
    fun antiAbuseAnchor(context: Context): String {
        val androidId = try {
            Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID) ?: ""
        } catch (_: Exception) { "" }
        val drm = try {
            val m = MediaDrm(UUID.fromString("edef8ba9-79d6-4ace-a3c8-27dcd51d21ed")) // Widevine
            try {
                // getPropertyByteArray can return null on L3 boxes — fall back to "" instead of
                // NPE-ing (the outer catch would swallow it, but make the null explicit).
                m.getPropertyByteArray(MediaDrm.PROPERTY_DEVICE_UNIQUE_ID)
                    ?.joinToString("") { "%02x".format(it.toInt() and 0xFF) }
                    ?: ""
            } finally {
                @Suppress("DEPRECATION") m.release()
            }
        } catch (_: Exception) { "" } // UnsupportedSchemeException / null on no-DRM boxes
        return "$androidId|$drm|${Build.MODEL ?: ""}"
    }

    /**
     * Returns [subUrl] with the stable device id and system form factor appended. Each marker is
     * independently idempotent: old stored URLs that already contain `device` gain `platform`,
     * while caller-supplied values are never replaced or duplicated.
     */
    fun withDevice(context: Context, subUrl: String): String {
        return withDeviceMetadata(
            subUrl = subUrl,
            deviceId = deviceId(context),
            platform = DeviceFormFactor.subscriptionPlatform(context),
        )
    }

    /**
     * Strips the device/platform markers from a /sub URL so a following [withDevice] re-stamps
     * the CURRENT device's identity. A shared "Поделиться подпиской" QR carries the SHARER's
     * ?device=…; importing it verbatim would make every device that scans it inherit one id and
     * silently bypass the account's server-side device cap (counted by distinct device id).
     * Idempotent on URLs that carry neither marker. Preserves the /sub token and any fragment.
     */
    fun stripDeviceMetadata(subUrl: String): String =
        removeQueryParameter(removeQueryParameter(subUrl, "device"), "platform")

    internal fun withDeviceMetadata(subUrl: String, deviceId: String, platform: String): String {
        var result = subUrl
        if (!hasQueryParameter(result, "device")) result = appendQueryParameter(result, "device", deviceId)
        if (!hasQueryParameter(result, "platform")) result = appendQueryParameter(result, "platform", platform)
        return result
    }

    private fun hasQueryParameter(url: String, name: String): Boolean {
        val query = url.substringBefore('#').substringAfter('?', "")
        return query.split('&').any { it.substringBefore('=') == name }
    }

    private fun appendQueryParameter(url: String, name: String, value: String): String {
        val fragmentIndex = url.indexOf('#')
        val base = if (fragmentIndex >= 0) url.substring(0, fragmentIndex) else url
        val fragment = if (fragmentIndex >= 0) url.substring(fragmentIndex) else ""
        val separator = when {
            !base.contains('?') -> "?"
            base.endsWith('?') || base.endsWith('&') -> ""
            else -> "&"
        }
        return "$base$separator$name=$value$fragment"
    }

    /** Removes every `name=…` query parameter, preserving the path, the other params, and the
     *  fragment. Drops the leading `?` when no parameters remain. */
    private fun removeQueryParameter(url: String, name: String): String {
        val fragmentIndex = url.indexOf('#')
        val fragment = if (fragmentIndex >= 0) url.substring(fragmentIndex) else ""
        val base = if (fragmentIndex >= 0) url.substring(0, fragmentIndex) else url
        val queryIndex = base.indexOf('?')
        if (queryIndex < 0) return url
        val path = base.substring(0, queryIndex)
        val kept = base.substring(queryIndex + 1)
            .split('&')
            .filter { it.isNotEmpty() && it.substringBefore('=') != name }
        return if (kept.isEmpty()) "$path$fragment" else "$path?${kept.joinToString("&")}$fragment"
    }

    /** Adds a /sub endpoint suffix before the query (never into device/platform values). */
    fun endpoint(remoteURL: String, suffix: String): String {
        require(suffix.matches(Regex("^[a-z]+$")))
        val withoutFragment = remoteURL.substringBefore('#')
        val query = withoutFragment.substringAfter('?', "")
        val base = withoutFragment.substringBefore('?').trimEnd('/')
        return buildString {
            append(base).append('/').append(suffix)
            if (query.isNotEmpty()) append('?').append(query)
        }
    }

    /** Extracts the sub token from a /sub/<token>[?device=…] URL (empty string if none). */
    fun token(remoteURL: String): String =
        remoteURL.substringAfterLast("/sub/", "").substringBefore('?').substringBefore('&')
}

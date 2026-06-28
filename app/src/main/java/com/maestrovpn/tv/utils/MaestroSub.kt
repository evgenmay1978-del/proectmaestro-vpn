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
 * protocol's creds are delivered through /sub). The id is a random UUID generated once per
 * install and stored in app-private prefs — it is NOT a hardware identifier (privacy, and it
 * regenerates on reinstall / data-clear, which is the intended "this is a new device"
 * semantics). Every /sub poll and the /claim call carries it via ?device=<id>.
 *
 * The owner admin logins (wapmix/wapmixx) are exempted server-side, so they are never capped.
 */
object MaestroSub {
    private const val PREFS = "maestro_device"
    private const val KEY = "device_id"

    /** Stable per-install device id; generated + persisted on first use. */
    fun deviceId(context: Context): String {
        val sp = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        sp.getString(KEY, null)?.let { return it }
        val id = UUID.randomUUID().toString()
        sp.edit().putString(KEY, id).apply()
        return id
    }

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
                m.getPropertyByteArray(MediaDrm.PROPERTY_DEVICE_UNIQUE_ID)
                    .joinToString("") { "%02x".format(it.toInt() and 0xFF) }
            } finally {
                @Suppress("DEPRECATION") m.release()
            }
        } catch (_: Exception) { "" } // UnsupportedSchemeException / null on no-DRM boxes
        return "$androidId|$drm|${Build.MODEL ?: ""}"
    }

    /** Returns subUrl with this install's device id appended (idempotent — no double-add). */
    fun withDevice(context: Context, subUrl: String): String {
        if (subUrl.contains("device=")) return subUrl
        val sep = if (subUrl.contains('?')) '&' else '?'
        return subUrl + sep + "device=" + deviceId(context)
    }

    /** Extracts the sub token from a /sub/<token>[?device=…] URL (empty string if none). */
    fun token(remoteURL: String): String =
        remoteURL.substringAfterLast("/sub/", "").substringBefore('?').substringBefore('&')
}

package com.maestrovpn.tv.utils

import android.content.Context
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

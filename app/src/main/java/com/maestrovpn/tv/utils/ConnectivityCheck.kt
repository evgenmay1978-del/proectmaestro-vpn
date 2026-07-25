package com.maestrovpn.tv.utils

import com.maestrovpn.tv.BuildConfig
import java.net.HttpURLConnection
import java.net.URL

/**
 * Backing check for the «Проверить соединение» button — does traffic actually flow right now?
 *
 * It used to be one request to google.com/generate_204, duplicated verbatim on the TV home and
 * the phone revolver menu. That endpoint answers "нет соединения" in exactly the situation this
 * product exists for: a restricted cell that only passes VK/OK/Яндекс (the WDTT scenario), where
 * the tunnel can be perfectly healthy while Google is unreachable.
 *
 * So probe OUR panel first — reaching it is what the subscription and update paths depend on —
 * and fall back to the neutral endpoint only when that fails, which still covers networks that
 * block the panel's non-standard port while general internet works. Either success counts.
 *
 * The probe deliberately does NOT call protect(), so with a tunnel up it travels through the
 * tunnel: the answer reflects whether the VPN passes traffic, not merely whether the radio is on.
 *
 * BLOCKING — call from Dispatchers.IO. Worst case (genuinely offline) is both timeouts in series.
 */
object ConnectivityCheck {
    private const val TIMEOUT_MS = 5_000
    private const val NEUTRAL_PROBE = "https://www.google.com/generate_204"
    private val PANEL_PROBE = BuildConfig.BACKEND_URL.trimEnd('/') + "/healthz"

    fun isOnline(): Boolean = probe(PANEL_PROBE) || probe(NEUTRAL_PROBE)

    private fun probe(url: String): Boolean = runCatching {
        (URL(url).openConnection() as HttpURLConnection).run {
            connectTimeout = TIMEOUT_MS
            readTimeout = TIMEOUT_MS
            try {
                responseCode in 200..399
            } finally {
                disconnect()
            }
        }
    }.getOrDefault(false)
}

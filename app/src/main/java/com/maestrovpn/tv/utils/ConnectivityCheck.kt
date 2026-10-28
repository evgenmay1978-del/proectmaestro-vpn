package com.maestrovpn.tv.utils

import com.maestrovpn.tv.BuildConfig
import java.net.HttpURLConnection
import java.net.URL

/**
 * Backing check for the «Проверить соединение» button — is the NETWORK alive right now?
 *
 * It used to be one request to google.com/generate_204, duplicated verbatim on the TV home and
 * the phone revolver menu. That endpoint answers "нет соединения" in exactly the situation this
 * product exists for: a restricted cell that only passes VK/OK/Яндекс (the WDTT scenario), where
 * the network can be perfectly healthy while Google is unreachable.
 *
 * So probe OUR panel first — reaching it is what the subscription and update paths depend on —
 * and fall back to the neutral endpoint only when that fails, which still covers networks that
 * block the panel's non-standard port while general internet works. Either success counts.
 *
 * WHAT IT MEASURES — the direct channel, not the tunnel (verified on the TV box 28.10.2026): the
 * hosting app is always exempt from its own VPN. app/src/main/java/com/maestrovpn/tv/bg/PackagePolicy.kt
 * leaves com.maestrovpn.tv out of every branch and the device rule table carries only the selected
 * uids, so an in-process request cannot be routed into the engine: "сеть есть" therefore never
 * proves that the tunnel passes traffic, and the wording returned by [verdict] says so instead of
 * pretending otherwise. Verifying the tunnel needs an in-engine endpoint (the D3 probe inbound,
 * kept out of this release line on purpose).
 *
 * BLOCKING — call from Dispatchers.IO. Worst case (genuinely offline) is both timeouts in series.
 */
object ConnectivityCheck {
    private const val TIMEOUT_MS = 5_000
    private const val NEUTRAL_PROBE = "https://www.google.com/generate_204"
    private val PANEL_PROBE = BuildConfig.BACKEND_URL.trimEnd('/') + "/healthz"

    fun isOnline(): Boolean = probe(PANEL_PROBE) || probe(NEUTRAL_PROBE)

    /**
     * Honest wording for the toast: [online] is the direct-channel answer above, [tunnelUp] is the
     * engine status the screen already knows. Kept here, not in the composable, so the wording is
     * testable and cannot drift back into "Соединение работает" while the tunnel is dead.
     */
    fun verdict(online: Boolean, tunnelUp: Boolean): String = when {
        online && tunnelUp -> "Сеть работает. Если сайты не открываются — смотрите туннель"
        online -> "Сеть работает"
        tunnelUp -> "Прямой доступ закрыт: туннель этой проверкой не виден"
        else -> "Нет соединения"
    }

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

package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.component.NeonAccountCard
import com.maestrovpn.tv.compose.model.visibleActiveProtocol
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * MaestroVPN home — the universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone. TV keeps its independent full-screen layout; phone uses the fixed carved
 * gold/wood frame, a full-size obsidian/emerald eye as the connect button, and a cylindrical
 * lower menu. Orange remains the protocol-selection and purchase accent.
 *
 * Layout adapts to the screen: a TV is WIDE, so it gets a two-zone LANDSCAPE layout —
 * hero (medallion + status + account) on the left, the menu on the right — which fits in
 * roughly one screen so there's almost nothing to scroll with the D-pad. Phone keeps the
 * hero fixed and scrolls only the content below the eye.
 */
@Composable
fun TvHomeScreen(
    statusText: String,
    connected: Boolean,
    connecting: Boolean = false,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String? = null,
    accountLogin: String? = null,
    daysLeft: Int? = null,
    accountExpires: String? = null,
    hasSubProfile: Boolean = false,
    hasOlcrtcCreds: Boolean = false,
    olcrtcProvider: String? = null, // "wbstream" | "telemost" — labels the single olcRTC chip
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit = {},
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit = {},
    onShareIos: () -> Unit = {},
    onScanQr: () -> Unit = {},
    onEnterTrial: () -> Unit = {},
) {
    val isTv = rememberIsTv()
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (isTv) runCatching { connectFocus.requestFocus() }
    }

    // Gentle entrance — crash-safe: the LaunchedEffect only flips a flag, never throws.
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { shown = true }
    val enter by animateFloatAsState(
        if (shown) 1f else 0f, tween(600, easing = FastOutSlowInEasing), label = "enter",
    )

    Surface(modifier = Modifier.fillMaxSize()) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .graphicsLayer {
                    // The phone hero is a fixed physical frame: it must never slide with the menu.
                    // Keep the existing TV entrance untouched.
                    alpha = if (isTv) enter else 1f
                    translationY = 0f
                }
                .padding(0.dp),   // и ТВ-эскиз, и телефон-эскиз = full-bleed
        ) {
            // ⛔ Particles убраны на ТВ (owner «без анимаций»): 20с infinite-clock + полноэкранная
            // перерисовка каждый кадр — дорого для 1 ГБ-боксов. Атмосферу даёт статичный glow+web ниже.

            if (isTv) {
                TvEskizHome(
                    statusText = statusText,
                    connected = connected,
                    protocols = protocols,
                    selected = selected,
                    activeProtocol = activeProtocol,
                    accountLogin = accountLogin,
                    daysLeft = daysLeft,
                    accountExpires = accountExpires,
                    hasSubProfile = hasSubProfile,
                    hasOlcrtcCreds = hasOlcrtcCreds,
                    olcrtcProvider = olcrtcProvider,
                    onToggleConnect = onToggleConnect,
                    onSelectProtocol = onSelectProtocol,
                    onSelectOlcrtc = onSelectOlcrtc,
                    onBuy = onBuy,
                    onEnterCode = onEnterCode,
                    onSplitTunnel = onSplitTunnel,
                    onShareIos = onShareIos,
                    onEnterTrial = onEnterTrial,
                    connectFocus = connectFocus,
                )
            } else {
                Mobile4DHome(
                    statusText = statusText,
                    connected = connected,
                    connecting = connecting,
                    protocols = protocols,
                    selected = selected,
                    activeProtocol = activeProtocol,
                    accountLogin = accountLogin,
                    daysLeft = daysLeft,
                    accountExpires = accountExpires,
                    hasSubProfile = hasSubProfile,
                    hasOlcrtcCreds = hasOlcrtcCreds,
                    olcrtcProvider = olcrtcProvider,
                    onToggleConnect = onToggleConnect,
                    onSelectProtocol = onSelectProtocol,
                    onSelectOlcrtc = onSelectOlcrtc,
                    onBuy = onBuy,
                    onEnterCode = onEnterCode,
                    onSplitTunnel = onSplitTunnel,
                    onShareIos = onShareIos,
                    onScanQr = onScanQr,
                    onEnterTrial = onEnterTrial,
                )
            }
        }
    }
}

/** Login + days-left glass card (bottom of the hero on both phone and TV). Computes the day
 *  colour/label — green / orange when ≤5 / red when expired / «Безлимит» for unlimited — from [daysLeft]. */
@Composable
internal fun AccountCard(login: String?, daysLeft: Int?, modifier: Modifier = Modifier, wood: Boolean = false, expires: String? = null) {
    val expired = daysLeft != null && daysLeft <= 0
    val low = daysLeft != null && daysLeft in 1..5
    val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else NeonGreen
    val daysText = when {
        daysLeft == null -> null
        expired -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит" // unlimited/owner accounts (~10y+) → don't show an absurd count
        else -> "Осталось $daysLeft ${daysWord(daysLeft)}" + (expires?.let { " · до $it" } ?: "")
    }
    NeonAccountCard(
        login = login,
        daysText = daysText,
        daysColor = daysColor,
        leadingIcon = Icons.Filled.Person,
        trailingIcon = Icons.Filled.CalendarMonth,
        wood = wood,
        modifier = modifier,
    )
}

/** PHONE status row under the medallion: a state dot + status text, plus the active protocol
 *  line when connected. Mirrors the HeroPane status semantics but flat (no breath clock). */
@Composable
internal fun PhoneStatusRow(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
) {
    // 1:1 к эталону owner: ОТКЛЮЧЕНО = красная точка + красный текст (было серебро/белый),
    // и строка протокола видна в ОБОИХ состояниях («Отключён: Vless-s3 • авто»).
    val stateRed = Color(0xFFFF4040)
    val protoMain = visibleActiveProtocol(activeProtocol, selected)
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 6.dp)
            .testTag("premium-status"),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(11.dp).clip(CircleShape).background(if (connected) NeonGreen else stateRed))
            Spacer(Modifier.width(9.dp))
            Text(
                statusText.uppercase(),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = if (connected) NeonGreen else stateRed,
            )
        }
        if (!protoMain.isNullOrBlank()) {
            val viaAuto = selected == "auto" && protoMain != "auto"
            val prefix = if (connected) "Подключён" else "Отключён"
            Text(
                if (viaAuto) "$prefix: ${protocolLabel(protoMain)}  •  авто" else "$prefix: ${protocolLabel(protoMain)}",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = MaestroOrange,
                textAlign = TextAlign.Center,
                maxLines = 2,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp),
            )
        }
    }
}

/** Small per-protocol glyph for the chips. */
internal fun protocolIcon(tag: String): ImageVector = when (tag) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Bolt
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Hub
    "anytls" -> Icons.Filled.Lock
    "olcrtc" -> Icons.Filled.Videocam
    "vk-turn" -> Icons.Filled.Videocam
    else -> Icons.Filled.Layers
}

/** Friendly chip label; "auto" is the urltest pick = the lowest-latency protocol. */
internal fun protocolLabel(tag: String): String = if (tag.startsWith("cdn:")) {
    com.maestrovpn.tv.whitelist.WhiteListSelection.label(tag) ?: "CDN"
} else when (tag) {
    "auto" -> "Авто"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "NaiveProxy"
    "anytls" -> "AnyTLS"
    "olcrtc" -> "olcRTC"
    "vk-turn" -> "WDTT"
    else -> tag.replaceFirstChar { it.uppercase() }
}

/** Short recommendation badge under each protocol chip (unified style). */
internal fun protocolBadge(tag: String): String = if (tag.startsWith("cdn:")) "При ограничениях" else when (tag) {
    "auto" -> "Рекомендуется"
    "vless", "vless-s3" -> "Оптимальный"
    "hysteria2" -> "Самый быстрый"
    "naive" -> "⚠ нестабильный"
    "anytls" -> "Без TLS-отпечатка"
    "olcrtc" -> "через Яндекс"
    "vk-turn" -> "через VK"
    "trojan" -> "Макс. защита"
    "shadowsocks" -> "Стабильный"
    else -> "Стабильный"
}

/** Russian plural of "день" for N: 1 день, 2-4 дня, 5-20 дней (incl. teens). */
internal fun daysWord(n: Int): String {
    if (n % 100 in 11..14) return "дней"
    return when (n % 10) {
        1 -> "день"
        2, 3, 4 -> "дня"
        else -> "дней"
    }
}

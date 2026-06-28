package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Speed
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
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.ChromeTile
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.NeonAccountCard
import com.maestrovpn.tv.compose.component.NeonChip
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import androidx.compose.runtime.rememberCoroutineScope

/**
 * MaestroVPN home — the universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone, restyled to the owner's reference (spiderinterfeis.png): deep near-black
 * with a green glow, the photoreal spider medallion as the hero connect button, dark
 * green-glass chips/cards with neon borders + icons. Orange is kept for SELECTION and
 * the primary CTA (buy).
 *
 * Layout adapts to the screen: a TV is WIDE, so it gets a two-zone LANDSCAPE layout —
 * hero (medallion + status + account) on the left, the menu on the right — which fits in
 * roughly one screen so there's almost nothing to scroll with the D-pad. A phone keeps
 * the familiar single scrolling column.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun TvHomeScreen(
    statusText: String,
    connected: Boolean,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String? = null,
    accountLogin: String? = null,
    daysLeft: Int? = null,
    hasSubProfile: Boolean = false,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
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
                .drawBehind {
                    // green glow — behind the medallion. On TV the medallion sits on the LEFT,
                    // so move the glow there; on a phone it stays upper-centre (matches the ref).
                    val center = if (isTv) {
                        Offset(size.width * 0.24f, size.height * 0.5f)
                    } else {
                        Offset(size.width * 0.5f, size.height * 0.28f)
                    }
                    val radius = size.maxDimension * 0.5f
                    drawCircle(
                        brush = Brush.radialGradient(
                            colors = listOf(
                                NeonGreen.copy(alpha = if (connected) 0.12f else 0.07f),
                                Color.Transparent,
                            ),
                            center = center, radius = radius,
                        ),
                        radius = radius, center = center,
                    )
                }
                .graphicsLayer {
                    alpha = enter
                    translationY = (1f - enter) * 36f
                }
                .padding(screenPadding(isTv)),
        ) {
            if (isTv) {
                // ── LANDSCAPE (TV): hero on the left, menu on the right ──
                Row(
                    modifier = Modifier.fillMaxSize(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    HeroPane(
                        statusText = statusText,
                        connected = connected,
                        activeProtocol = activeProtocol,
                        selected = selected,
                        accountLogin = accountLogin,
                        daysLeft = daysLeft,
                        onToggleConnect = onToggleConnect,
                        connectFocus = connectFocus,
                        modifier = Modifier.weight(0.42f),
                    )
                    Spacer(Modifier.width(28.dp))
                    MenuPane(
                        protocols = protocols,
                        selected = selected,
                        isTv = true,
                        onSelectProtocol = onSelectProtocol,
                        onBuy = onBuy,
                        onEnterCode = onEnterCode,
                        onSplitTunnel = onSplitTunnel,
                        onShareIos = onShareIos,
                        onScanQr = onScanQr,
                        onEnterTrial = onEnterTrial,
                        showTrial = !hasSubProfile,
                        // fillMaxHeight + scroll is a safety net for short (720p) TVs; the menu
                        // normally fits without scrolling.
                        modifier = Modifier
                            .weight(0.58f)
                            .fillMaxHeight()
                            .verticalScroll(rememberScrollState()),
                    )
                }
            } else {
                // ── PORTRAIT (phone): single scrolling column ──
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .verticalScroll(rememberScrollState()),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    HeroPane(
                        statusText = statusText,
                        connected = connected,
                        activeProtocol = activeProtocol,
                        selected = selected,
                        accountLogin = accountLogin,
                        daysLeft = daysLeft,
                        onToggleConnect = onToggleConnect,
                        connectFocus = connectFocus,
                        modifier = Modifier.fillMaxWidth().widthIn(max = 600.dp),
                    )
                    Spacer(Modifier.height(14.dp))
                    MenuPane(
                        protocols = protocols,
                        selected = selected,
                        isTv = false,
                        onSelectProtocol = onSelectProtocol,
                        onBuy = onBuy,
                        onEnterCode = onEnterCode,
                        onSplitTunnel = onSplitTunnel,
                        onShareIos = onShareIos,
                        onScanQr = onScanQr,
                        onEnterTrial = onEnterTrial,
                        showTrial = !hasSubProfile,
                        modifier = Modifier.fillMaxWidth().widthIn(max = 600.dp),
                    )
                }
            }
        }
    }
}

/** The hero zone: wordmark + the spider medallion connect button + status + account. */
@Composable
private fun HeroPane(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
    accountLogin: String?,
    daysLeft: Int?,
    onToggleConnect: () -> Unit,
    connectFocus: FocusRequester,
    modifier: Modifier = Modifier,
) {
    val isTv = rememberIsTv()
    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        // Brand wordmark — the owner's spiderweb logo (orange "Maestro" / green "VPN" + a
        // hanging spider). On TV the width = the medallion (252.dp) so the hero reads as one
        // centred unit; on a phone it's a bit larger (308.dp). Height follows the ~2.15:1 aspect.
        // Transparent PNG → blends on the dark bg.
        Image(
            painter = painterResource(R.drawable.maestro_wordmark),
            contentDescription = "MaestroVPN",
            contentScale = ContentScale.Fit,
            modifier = Modifier
                .width(if (isTv) 252.dp else 308.dp)
                .height(if (isTv) 117.dp else 143.dp),
        )
        Spacer(Modifier.height(18.dp))

        SpiderMedallion(
            connected = connected,
            onToggle = onToggleConnect,
            focusRequester = connectFocus,
        )

        Spacer(Modifier.height(14.dp))
        // status with a state dot
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .size(11.dp)
                    .clip(CircleShape)
                    .background(if (connected) NeonGreen else MaestroSilver),
            )
            Spacer(Modifier.width(9.dp))
            Text(
                statusText.uppercase(),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = if (connected) NeonGreen else MaterialTheme.colorScheme.onSurface,
            )
        }

        // Account card — login + days left (from the panel /sub/<token>/info).
        if (!accountLogin.isNullOrBlank() || daysLeft != null) {
            Spacer(Modifier.height(12.dp))
            val expired = daysLeft != null && daysLeft <= 0
            val low = daysLeft != null && daysLeft in 1..5
            val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else NeonGreen
            val daysText = when {
                daysLeft == null -> null
                expired -> "Подписка истекла"
                else -> "Осталось $daysLeft ${daysWord(daysLeft)}"
            }
            NeonAccountCard(
                login = accountLogin,
                daysText = daysText,
                daysColor = daysColor,
                leadingIcon = Icons.Filled.Person,
                trailingIcon = Icons.Filled.CalendarMonth,
                modifier = Modifier.widthIn(min = 240.dp),
            )
        }

        // Active protocol — the outbound actually carrying traffic right now (orange).
        if (connected && !activeProtocol.isNullOrBlank()) {
            Spacer(Modifier.height(10.dp))
            val viaAuto = selected == "auto" && activeProtocol != "auto"
            Text(
                if (viaAuto) "Подключён: ${protocolLabel(activeProtocol)}  •  авто" else "Подключён: ${protocolLabel(activeProtocol)}",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = MaestroOrange,
            )
        }
    }
}

/** The menu zone: protocol picker + buy + secondary actions + contacts. */
@Composable
private fun MenuPane(
    protocols: List<String>,
    selected: String?,
    isTv: Boolean,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit,
    onShareIos: () -> Unit,
    onScanQr: () -> Unit,
    onEnterTrial: () -> Unit = {},
    showTrial: Boolean = false,
    modifier: Modifier = Modifier,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val open: (String) -> Unit = { url ->
        runCatching {
            ctx.startActivity(
                Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    }
    val onUpdate: () -> Unit = {
        (ctx as? Activity)?.let { act ->
            scope.launch(Dispatchers.IO) { runCatching { Vendor.checkUpdate(act, true) } }
        }
    }

    // Column count adapts to the surface: a phone is NARROW → 2 columns (long RU labels
    // fit with no mid-word break); a TV menu pane is WIDE → 3 columns so the chrome chips
    // fill it instead of stretching huge.
    val cols = if (isTv) 3 else 2

    Column(modifier = modifier) {
        // ── ПРОТОКОЛ — equal-width chrome chips (2 col phone / 3 col TV) ──
        if (protocols.isNotEmpty()) {
            SectionLabel("ПРОТОКОЛ")
            Spacer(Modifier.height(10.dp))
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                protocols.chunked(cols).forEach { row ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        row.forEach { p ->
                            NeonChip(
                                label = protocolLabel(p),
                                onClick = { onSelectProtocol(p) },
                                modifier = Modifier.weight(1f).heightIn(min = 60.dp),
                                icon = protocolIcon(p),
                                selected = p == selected,
                            )
                        }
                        // keep columns equal-width when the last row is short
                        repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                    }
                }
            }
            Spacer(Modifier.height(16.dp))
        }

        // Free-trial CTA — shown ONLY when there is no MaestroVPN sub profile (!hasSubProfile), so an
        // active subscriber can't tap it and have their paid profile replaced by a 2-day trial. Keying
        // on the LOCAL profile (not a panel field) means a transient timeout never re-shows it to a payer.
        if (showTrial) {
            GlossyButton(
                label = "🎁 Попробовать 2 дня бесплатно",
                onClick = onEnterTrial,
                accent = NeonGreen,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(10.dp))
        }

        GlossyButton(
            label = "Купить подписку",
            onClick = onBuy,
            accent = MaestroOrange,
            icon = Icons.Filled.ShoppingCart,
            modifier = Modifier.fillMaxWidth(),
        )

        // ── secondary actions — chrome tiles (icon on top, short label); update full-width ──
        Spacer(Modifier.height(12.dp))
        val tiles = buildList<Triple<String, ImageVector, () -> Unit>> {
            add(Triple("Ввести код", Icons.Filled.Search, onEnterCode))
            if (!isTv) add(Triple("Сканировать QR", Icons.Filled.QrCode2, onScanQr))
            add(Triple("Приложения VPN", Icons.Filled.Public, onSplitTunnel))
            add(Triple("Поделиться", Icons.Filled.Share, onShareIos))
        }
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            tiles.chunked(cols).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    row.forEach { a ->
                        ChromeTile(a.first, a.second, a.third, Modifier.weight(1f).heightIn(min = 86.dp))
                    }
                    // keep columns equal-width when the last row is short
                    repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                }
            }
        }
        Spacer(Modifier.height(10.dp))
        NeonChip(
            label = "Обновить приложение",
            onClick = onUpdate,
            modifier = Modifier.fillMaxWidth().heightIn(min = 54.dp),
            icon = Icons.Filled.CloudDownload,
        )

        // ── Контакты ──────────────────────────────────────────────────
        Spacer(Modifier.height(20.dp))
        SectionLabel("КОНТАКТЫ")
        Spacer(Modifier.height(10.dp))
        GlossyButton(
            label = "8 977 811-65-64",
            onClick = { open("tel:+79778116564") },
            accent = NeonGreen,
            icon = Icons.Filled.Call,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(10.dp))
        Text(
            "Если я не ответил на звонок — обязательно напишите в любом из мессенджеров 👇",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp),
        )
        Spacer(Modifier.height(10.dp))
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            NeonChip("Telegram", { open("https://t.me/wapmixx") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Send, iconTint = Color(0xFF2AABEE))
            NeonChip("WhatsApp", { open("https://wa.me/79778116564") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Chat, iconTint = Color(0xFF25D366))
            NeonChip("МАКС", { open("https://max.ru/") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Forum, iconTint = Color(0xFF2787F5))
        }
        Spacer(Modifier.height(20.dp))
    }
}

/** Small per-protocol glyph for the chips. */
private fun protocolIcon(tag: String): ImageVector = when (tag) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Bolt
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Hub
    "anytls" -> Icons.Filled.Lock
    else -> Icons.Filled.Layers
}

/** Friendly chip label; "auto" is the urltest pick = the lowest-latency protocol. */
private fun protocolLabel(tag: String): String = when (tag) {
    "auto" -> "Авто"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "NaiveProxy"
    "anytls" -> "AnyTLS"
    else -> tag.replaceFirstChar { it.uppercase() }
}

/** Russian plural of "день" for N: 1 день, 2-4 дня, 5-20 дней (incl. teens). */
private fun daysWord(n: Int): String {
    if (n % 100 in 11..14) return "дней"
    return when (n % 10) {
        1 -> "день"
        2, 3, 4 -> "дня"
        else -> "дней"
    }
}

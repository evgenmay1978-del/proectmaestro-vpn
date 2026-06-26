package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
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
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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
 * Volumetric "MaestroVPN" wordmark — each glyph is genuinely EXTRUDED (a stack of dark
 * side faces under a beveled, specular-lit front face), giving real depth. "Maestro"
 * orange, "VPN" silver. Static (no per-frame spin) so it's free on a weak TV box.
 */
@Composable
private fun AnimatedLogo(modifier: Modifier = Modifier) {
    val word = "MaestroVPN"
    Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
        word.forEachIndexed { i, ch ->
            val isVpn = i >= word.length - 3
            Letter3D(ch, if (isVpn) MaestroSilver else MaestroOrange)
        }
    }
}

@Composable
private fun Letter3D(ch: Char, base: Color) {
    val depth = 8
    val side = lerp(base, Color.Black, 0.5f)
    Box(contentAlignment = Alignment.Center) {
        for (d in depth downTo 1) {
            Text(
                ch.toString(),
                fontSize = 46.sp,
                fontWeight = FontWeight.Black,
                color = lerp(side, Color.Black, d / (depth * 1.6f)),
                modifier = Modifier.graphicsLayer { translationX = d * 1.7f; translationY = d * 1.7f },
            )
        }
        Text(
            ch.toString(),
            fontSize = 46.sp,
            fontWeight = FontWeight.Black,
            style = TextStyle(
                brush = Brush.verticalGradient(
                    0f to lerp(base, Color.White, 0.85f),
                    0.18f to lerp(base, Color.White, 0.25f),
                    0.55f to base,
                    1f to lerp(base, Color.Black, 0.28f),
                ),
                shadow = Shadow(Color.Black.copy(alpha = 0.6f), Offset(0f, 4f), 7f),
            ),
        )
    }
}

/**
 * MaestroVPN home — the universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone, restyled to the owner's reference (spiderinterfeis.png): deep near-black
 * with a green glow, the photoreal spider medallion as the hero connect button, dark
 * green-glass chips/cards with neon borders + icons. Orange is kept for SELECTION and
 * the primary CTA (buy). The column scrolls; chips wrap (FlowRow).
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
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit = {},
    onShareIos: () -> Unit = {},
    onScanQr: () -> Unit = {},
) {
    val isTv = rememberIsTv()
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (isTv) runCatching { connectFocus.requestFocus() }
    }
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val open: (String) -> Unit = { url ->
        runCatching {
            ctx.startActivity(
                Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    }

    // Gentle entrance — crash-safe: the LaunchedEffect only flips a flag, never throws.
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { shown = true }
    val enter by animateFloatAsState(
        if (shown) 1f else 0f, tween(600, easing = FastOutSlowInEasing), label = "enter",
    )

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .drawBehind {
                    // green glow centred behind the medallion (matches the reference)
                    val center = Offset(size.width * 0.5f, size.height * 0.28f)
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
                .verticalScroll(rememberScrollState())
                .graphicsLayer {
                    alpha = enter
                    translationY = (1f - enter) * 36f
                }
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            AnimatedLogo()
            Spacer(Modifier.height(22.dp))

            // ── Hero: the spider medallion connect button ──────────────────
            SpiderMedallion(
                connected = connected,
                onToggle = onToggleConnect,
                focusRequester = connectFocus,
            )

            Spacer(Modifier.height(16.dp))
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
                Spacer(Modifier.height(14.dp))
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

            // content width cap so the grids stay tidy on a wide TV (centred column).
            // 600dp gives three equal columns enough room without crowding the labels.
            val contentMod = Modifier.fillMaxWidth().widthIn(max = 600.dp)

            // ── ПРОТОКОЛ — 3-column equal-width grid ──
            if (protocols.isNotEmpty()) {
                Spacer(Modifier.height(24.dp))
                SectionLabel("ПРОТОКОЛ")
                Spacer(Modifier.height(10.dp))
                Column(contentMod, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    protocols.chunked(3).forEach { row ->
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            row.forEach { p ->
                                NeonChip(
                                    label = protocolLabel(p),
                                    onClick = { onSelectProtocol(p) },
                                    modifier = Modifier.weight(1f).heightIn(min = 54.dp),
                                    icon = protocolIcon(p),
                                    selected = p == selected,
                                )
                            }
                            // keep columns equal-width when the last row is short
                            repeat(3 - row.size) { Spacer(Modifier.weight(1f)) }
                        }
                    }
                }
            }

            Spacer(Modifier.height(22.dp))
            GlossyButton(
                label = "Купить подписку",
                onClick = onBuy,
                accent = MaestroOrange,
                icon = Icons.Filled.ShoppingCart,
                modifier = contentMod,
            )

            // ── secondary actions — 3-column equal-width grid ──
            Spacer(Modifier.height(10.dp))
            val onUpdate: () -> Unit = {
                (ctx as? Activity)?.let { act ->
                    scope.launch(Dispatchers.IO) { runCatching { Vendor.checkUpdate(act, true) } }
                }
            }
            val actions = buildList<Triple<String, ImageVector, () -> Unit>> {
                add(Triple("Ввести код подписки", Icons.Filled.Search, onEnterCode))
                if (!isTv) add(Triple("Сканировать QR", Icons.Filled.QrCode2, onScanQr))
                add(Triple("Приложения через VPN", Icons.Filled.Public, onSplitTunnel))
                add(Triple("Поделиться подпиской", Icons.Filled.Share, onShareIos))
                add(Triple("Обновить приложение", Icons.Filled.CloudDownload, onUpdate))
            }
            Column(contentMod, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                actions.chunked(3).forEach { row ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        row.forEach { a ->
                            NeonChip(a.first, a.third, Modifier.weight(1f).heightIn(min = 54.dp), icon = a.second)
                        }
                        // keep columns equal-width when the last row is short
                        repeat(3 - row.size) { Spacer(Modifier.weight(1f)) }
                    }
                }
            }

            // ── Контакты ──────────────────────────────────────────────────
            Spacer(Modifier.height(24.dp))
            SectionLabel("КОНТАКТЫ")
            Spacer(Modifier.height(10.dp))
            GlossyButton(
                label = "8 977 811-65-64",
                onClick = { open("tel:+79778116564") },
                accent = NeonGreen,
                icon = Icons.Filled.Call,
                modifier = contentMod,
            )
            Spacer(Modifier.height(10.dp))
            Text(
                "Если я не ответил на звонок — обязательно напишите в любом из мессенджеров 👇",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.Center,
                modifier = Modifier.widthIn(max = 360.dp).padding(horizontal = 12.dp),
            )
            Spacer(Modifier.height(10.dp))
            Row(contentMod, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                NeonChip("Telegram", { open("https://t.me/wapmixx") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Send, iconTint = Color(0xFF2AABEE))
                NeonChip("WhatsApp", { open("https://wa.me/79778116564") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Chat, iconTint = Color(0xFF25D366))
                NeonChip("МАКС", { open("https://max.ru/") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Forum, iconTint = Color(0xFF2787F5))
            }
            Spacer(Modifier.height(20.dp))
        }
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
    "auto" -> "Авто (лучший пинг)"
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

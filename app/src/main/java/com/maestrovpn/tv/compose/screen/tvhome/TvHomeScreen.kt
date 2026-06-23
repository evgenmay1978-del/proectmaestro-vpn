package com.maestrovpn.tv.compose.screen.tvhome

import android.content.Intent
import android.net.Uri
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
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
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import android.app.Activity
import androidx.compose.animation.core.keyframes
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextAlign
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import com.maestrovpn.tv.compose.rememberIsLowRam
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver

private val ConnGreen = Color(0xFF2FBF71)
private val PlateDark = Color(0xFF26262B)

/** Vertical "light-top → dark-bottom" sheen that makes a flat plate read as raised/3D. */
private fun raisedBrush(base: Color) = Brush.verticalGradient(
    listOf(lerp(base, Color.White, 0.20f), base, lerp(base, Color.Black, 0.22f)),
)

/** A volumetric (raised gradient + drop-shadow) button. Keeps Material focus/ripple for D-pad. */
@Composable
private fun VolButton(
    text: String,
    onClick: () -> Unit,
    base: Color,
    modifier: Modifier = Modifier,
    bold: Boolean = true,
) {
    val shape = RoundedCornerShape(16.dp)
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        if (pressed) 0.97f else if (focused) 1.06f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "vbScale",
    )
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 22.dp, vertical = 12.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = Color.Transparent,
            contentColor = Color.White,
        ),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .shadow(if (focused) 16.dp else 8.dp, shape, clip = false)
            .background(raisedBrush(base), shape)
            .border(if (focused) 2.dp else 0.dp, if (focused) Color.White else Color.Transparent, shape),
    ) { Text(text, fontWeight = if (bold) FontWeight.Bold else FontWeight.Medium) }
}

/**
 * Volumetric "MaestroVPN" wordmark. Each glyph spins a full turn around its own
 * vertical axis IN SEQUENCE — a wave that sweeps from the first letter to the last,
 * then holds, then repeats (slow + classy). Each letter is genuinely EXTRUDED (see
 * [Letter3D]) so it has real depth, not just a flat gradient. "Maestro" orange,
 * "VPN" silver.
 */
@Composable
private fun AnimatedLogo(modifier: Modifier = Modifier, lowRam: Boolean = false) {
    val word = "MaestroVPN"
    // Low-RAM (≈1GB Sony/TCL): render a STATIC wordmark — no rememberInfiniteTransition, no
    // per-frame rotationY. The perpetual 90-glyph spin is the biggest GPU/CPU sink on the home
    // screen and is wasted on a weak box (letters keep their 3D depth, they just don't rotate).
    if (lowRam) {
        Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
            word.forEachIndexed { i, ch ->
                val isVpn = i >= word.length - 3
                Letter3D(ch, if (isVpn) MaestroSilver else MaestroOrange) { 0f }
            }
        }
        return
    }
    val stepMs = 230    // slower: delay between consecutive letters kicking off
    val spinMs = 1400   // slower: one letter's full 360° turn
    val pauseMs = 2200  // longer hold after the wave before it repeats
    val total = word.length * stepMs + spinMs + pauseMs
    val tr = rememberInfiniteTransition(label = "logo")
    Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
        word.forEachIndexed { i, ch ->
            val start = i * stepMs
            val rotState = tr.animateFloat(
                initialValue = 0f,
                targetValue = 360f,
                animationSpec = infiniteRepeatable(
                    animation = keyframes {
                        durationMillis = total
                        // hold facing forward until this letter's turn, spin once, then hold
                        0f at start using FastOutSlowInEasing
                        360f at (start + spinMs)
                        360f at total
                    },
                    repeatMode = RepeatMode.Restart,
                ),
                label = "rot$i",
            )
            val isVpn = i >= word.length - 3
            // pass the animated value as a lambda → read in graphicsLayer (draw phase),
            // so the 9 layered glyphs per letter compose once and only re-draw, not recompose.
            Letter3D(ch, if (isVpn) MaestroSilver else MaestroOrange) { rotState.value }
        }
    }
}

/**
 * One EXTRUDED 3D glyph: a stack of dark "side" faces offset down-right builds the
 * letter's thickness, with a beveled, specular-lit front face on top; the whole stack
 * is spun around its Y axis by [rot]. That real depth is what the flat version lacked.
 */
@Composable
private fun Letter3D(ch: Char, base: Color, rot: () -> Float) {
    val depth = 8
    val side = lerp(base, Color.Black, 0.5f)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier.graphicsLayer {
            rotationY = rot()
            cameraDistance = 18f * density
        },
    ) {
        // extruded body — copies offset down-right, fading dark→darker, form the thickness
        for (d in depth downTo 1) {
            Text(
                ch.toString(),
                fontSize = 46.sp,
                fontWeight = FontWeight.Black,
                color = lerp(side, Color.Black, d / (depth * 1.6f)),
                modifier = Modifier.graphicsLayer { translationX = d * 1.7f; translationY = d * 1.7f },
            )
        }
        // front face — bright top → dark bottom with a thin specular band + drop shadow
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
 * MaestroVPN home — universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone. Hero is a big ROUND power button in the centre (brutal look): it's
 * the focus target on TV and the obvious tap target on a phone. Plain Material 3
 * (clickable by touch AND D-pad-focusable, unlike tv-material3). The column
 * scrolls so nothing clips; protocol chips wrap (FlowRow); secondary actions are
 * lighter outlined buttons so the connect button stays dominant.
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
    val lowRam = rememberIsLowRam()
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (isTv) runCatching { connectFocus.requestFocus() }
    }
    val accent = if (connected) ConnGreen else MaestroOrange
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val open: (String) -> Unit = { url ->
        runCatching {
            ctx.startActivity(
                Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    }

    // ── Motion: keep the screen alive (tasteful, not gaudy) ─────────────────
    val infinite = rememberInfiniteTransition(label = "home")
    val pulse by infinite.animateFloat(
        0f, 1f,
        infiniteRepeatable(tween(1500, easing = FastOutSlowInEasing), RepeatMode.Reverse),
        label = "pulse",
    )
    val ringRot by infinite.animateFloat(
        0f, 360f,
        infiniteRepeatable(tween(7000, easing = LinearEasing)),
        label = "ringRot",
    )
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
                    drawCircle(
                        brush = Brush.radialGradient(
                            colors = listOf(accent.copy(alpha = 0.05f + 0.06f * pulse), Color.Transparent),
                            center = Offset(size.width * 0.5f, size.height * 0.30f),
                            radius = size.maxDimension * 0.55f,
                        ),
                        radius = size.maxDimension * 0.55f,
                        center = Offset(size.width * 0.5f, size.height * 0.30f),
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
            // Brand wordmark — volumetric; letters spin in turn (STATIC on low-RAM boxes).
            AnimatedLogo(lowRam = lowRam)
            Spacer(Modifier.height(28.dp))

            // ── Round power button (the hero) — a living "orb" ──────────────
            val press = remember { MutableInteractionSource() }
            val pressed by press.collectIsPressedAsState()
            val heroFocused by press.collectIsFocusedAsState()
            val btnScale by animateFloatAsState(
                if (pressed) 0.93f else if (heroFocused) 1.05f else 1f,
                tween(140, easing = FastOutSlowInEasing), label = "btnScale",
            )
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(240.dp)
                    .graphicsLayer { scaleX = btnScale; scaleY = btnScale }
                    .drawBehind {
                        val c = Offset(size.width / 2f, size.height / 2f)
                        val orbR = 94.dp.toPx()
                        // 1) soft breathing halo — a glow RING hugging the orb (not behind it)
                        drawCircle(
                            brush = Brush.radialGradient(
                                0.00f to Color.Transparent,
                                0.66f to Color.Transparent,
                                0.84f to accent.copy(alpha = 0.28f + 0.16f * pulse + (if (heroFocused) 0.24f else 0f)),
                                1.00f to Color.Transparent,
                                center = c,
                                radius = orbR * 1.5f,
                            ),
                            radius = orbR * 1.5f,
                            center = c,
                        )
                        // 2) slow rotating highlight ring — smooth (transparent at both seam ends)
                        rotate(ringRot, pivot = c) {
                            drawArc(
                                brush = Brush.sweepGradient(
                                    listOf(Color.Transparent, accent, Color.Transparent),
                                    center = c,
                                ),
                                startAngle = 0f,
                                sweepAngle = 360f,
                                useCenter = false,
                                topLeft = Offset(c.x - orbR * 1.14f, c.y - orbR * 1.14f),
                                size = Size(orbR * 2.28f, orbR * 2.28f),
                                style = Stroke(width = 3.dp.toPx()),
                            )
                        }
                        // 3) the orb — a 3D sphere with an offset (top-left) light source
                        drawCircle(
                            brush = Brush.radialGradient(
                                colors = listOf(
                                    lerp(accent, Color.White, 0.45f),
                                    accent,
                                    lerp(accent, Color.Black, 0.30f),
                                ),
                                center = Offset(c.x - orbR * 0.34f, c.y - orbR * 0.40f),
                                radius = orbR * 1.55f,
                            ),
                            radius = orbR,
                            center = c,
                        )
                    },
            ) {
                // transparent click/focus layer on top of the drawn orb (no Material shadow)
                Button(
                    onClick = onToggleConnect,
                    shape = CircleShape,
                    contentPadding = PaddingValues(0.dp),
                    interactionSource = press,
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Color.Transparent,
                        contentColor = Color.White,
                    ),
                    elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
                    modifier = Modifier
                        .size(188.dp)
                        .focusRequester(connectFocus),
                ) {
                    Icon(
                        Icons.Filled.PowerSettingsNew,
                        contentDescription = if (connected) "Отключить" else "Подключить",
                        tint = Color.White,
                        modifier = Modifier.size(92.dp),
                    )
                }
            }

            Spacer(Modifier.height(18.dp))
            // status with a state dot
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(11.dp)
                        .graphicsLayer {
                            val s = if (connected) 1f + 0.35f * pulse else 1f
                            scaleX = s; scaleY = s
                        }
                        .clip(CircleShape)
                        .background(if (connected) ConnGreen else MaestroSilver),
                )
                Spacer(Modifier.width(9.dp))
                Text(
                    statusText.uppercase(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = if (connected) ConnGreen else MaterialTheme.colorScheme.onSurface,
                )
            }

            // Account card — the customer's login + how many days are left on their
            // subscription (fetched from the panel /sub/<token>/info; null = unknown).
            if (!accountLogin.isNullOrBlank() || daysLeft != null) {
                Spacer(Modifier.height(14.dp))
                val expired = daysLeft != null && daysLeft <= 0
                val low = daysLeft != null && daysLeft in 1..5
                val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else ConnGreen
                Surface(
                    shape = RoundedCornerShape(14.dp),
                    color = PlateDark,
                    modifier = Modifier.widthIn(min = 220.dp),
                ) {
                    Column(
                        modifier = Modifier.padding(horizontal = 18.dp, vertical = 10.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        if (!accountLogin.isNullOrBlank()) {
                            Text(
                                "Аккаунт: $accountLogin",
                                style = MaterialTheme.typography.titleSmall,
                                fontWeight = FontWeight.Bold,
                                color = Color.White,
                            )
                        }
                        if (daysLeft != null) {
                            Spacer(Modifier.height(3.dp))
                            Text(
                                if (expired) "Подписка истекла" else "Осталось $daysLeft ${daysWord(daysLeft)}",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.Black,
                                color = daysColor,
                            )
                        }
                    }
                }
            }

            // Active protocol — the outbound actually carrying traffic right now.
            // With "Авто" the urltest re-picks the lowest-latency protocol live, so
            // this resolves the selector→urltest chain to the real leaf and updates
            // whenever auto switches.
            if (connected && !activeProtocol.isNullOrBlank()) {
                Spacer(Modifier.height(10.dp))
                val viaAuto = selected == "auto" && activeProtocol != "auto"
                Text(
                    if (viaAuto) {
                        "Подключён: ${protocolLabel(activeProtocol)}  •  авто"
                    } else {
                        "Подключён: ${protocolLabel(activeProtocol)}"
                    },
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaestroOrange,
                )
            }

            if (protocols.isNotEmpty()) {
                Spacer(Modifier.height(26.dp))
                Text(
                    "ПРОТОКОЛ",
                    style = MaterialTheme.typography.labelLarge,
                    color = MaestroSilver,
                    letterSpacing = 2.sp,
                )
                Spacer(Modifier.height(10.dp))
                FlowRow(
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    protocols.forEach { protocol ->
                        VolButton(
                            text = protocolLabel(protocol),
                            onClick = { onSelectProtocol(protocol) },
                            base = if (protocol == selected) MaestroOrange else PlateDark,
                            bold = protocol == selected,
                        )
                    }
                }
            }

            Spacer(Modifier.height(28.dp))
            // primary subscribe action — the orange raised plate
            VolButton(
                text = "Купить подписку",
                onClick = onBuy,
                base = MaestroOrange,
                modifier = Modifier.widthIn(min = 260.dp),
            )

            // secondary actions — raised dark plates, so the orb + orange buy still lead
            Spacer(Modifier.height(10.dp))
            VolButton("Ввести код подписки", onEnterCode, PlateDark, Modifier.widthIn(min = 260.dp), bold = false)
            // QR scan: convenient on a phone (camera); hidden on TV (no camera, D-pad).
            if (!isTv) {
                Spacer(Modifier.height(8.dp))
                VolButton("Сканировать QR", onScanQr, PlateDark, Modifier.widthIn(min = 260.dp), bold = false)
            }
            Spacer(Modifier.height(8.dp))
            VolButton("Приложения через VPN", onSplitTunnel, PlateDark, Modifier.widthIn(min = 260.dp), bold = false)
            Spacer(Modifier.height(8.dp))
            VolButton("Поделиться подпиской", onShareIos, PlateDark, Modifier.widthIn(min = 260.dp), bold = false)
            Spacer(Modifier.height(10.dp))
            // Update — prominent. The auto OTA dialog can be swiped away, and a plain
            // re-open doesn't re-show it (the launch check only runs on a COLD start), so
            // many users get stuck on an old build. This button re-checks on demand and
            // always pops a fresh dialog (or "у вас последняя версия"). Vendor.checkUpdate
            // does blocking network → run it off the main thread.
            VolButton(
                text = "⬇️ Обновить приложение",
                onClick = {
                    (ctx as? Activity)?.let { act ->
                        scope.launch(Dispatchers.IO) { runCatching { Vendor.checkUpdate(act, true) } }
                    }
                },
                base = MaestroOrange,
                modifier = Modifier.widthIn(min = 260.dp),
            )

            // ── Контакты ──────────────────────────────────────────────────
            Spacer(Modifier.height(26.dp))
            Text(
                "КОНТАКТЫ",
                style = MaterialTheme.typography.labelLarge,
                color = MaestroSilver,
                letterSpacing = 2.sp,
            )
            Spacer(Modifier.height(10.dp))
            VolButton(
                text = "📞 8 977 811-65-64",
                onClick = { open("tel:+79778116564") },
                base = ConnGreen,
                modifier = Modifier.widthIn(min = 260.dp),
            )
            Spacer(Modifier.height(10.dp))
            Text(
                "Если я не ответил на звонок — обязательно напишите в любом из мессенджеров 👇",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.Center,
                modifier = Modifier
                    .widthIn(max = 340.dp)
                    .padding(horizontal = 12.dp),
            )
            Spacer(Modifier.height(10.dp))
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                VolButton("Telegram", { open("https://t.me/wapmixx") }, PlateDark, bold = false)
                VolButton("WhatsApp", { open("https://wa.me/79778116564") }, PlateDark, bold = false)
                VolButton("МАКС", { open("https://max.ru/") }, PlateDark, bold = false)
            }
        }
    }
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

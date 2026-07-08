package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.material.icons.filled.NetworkCheck
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import android.widget.Toast
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.withContext
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
import com.maestrovpn.tv.compose.rememberIsLowRam
import com.maestrovpn.tv.compose.rememberIsTv
import kotlin.math.floor
import kotlin.math.sin
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
    hasOlcrtcCreds: Boolean = false,
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
    val lowRam = rememberIsLowRam()
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
                        Offset(size.width * 0.5f, size.height * 0.40f)   // ON the medallion
                    }
                    // Glow ONLY around the central element (owner: свечение только вокруг центра).
                    // Tight radius (was half the screen → bled onto account/status).
                    val radius = if (isTv) size.maxDimension * 0.45f else size.minDimension * 0.52f
                    drawCircle(
                        brush = Brush.radialGradient(
                            colors = listOf(
                                NeonGreen.copy(alpha = if (connected) 0.11f else 0.06f),
                                NeonGreen.copy(alpha = if (connected) 0.035f else 0.02f),
                                Color.Transparent,
                            ),
                            center = center, radius = radius,
                        ),
                        radius = radius, center = center,
                    )
                    // Faint decorative SPIDERWEB around the glow — a few radial spokes + concentric
                    // rings at very low alpha for atmosphere/depth. Also static (no per-frame clock).
                    val webR = size.minDimension * (if (isTv) 0.46f else 0.58f)
                    val webA = if (connected) 0.055f else 0.038f
                    val spokes = 12
                    for (i in 0 until spokes) {
                        val a = (i * 2f * Math.PI / spokes).toFloat()
                        drawLine(
                            color = NeonGreen.copy(alpha = webA),
                            start = center,
                            end = Offset(center.x + kotlin.math.cos(a) * webR, center.y + kotlin.math.sin(a) * webR),
                            strokeWidth = 1f,
                        )
                    }
                    for (ring in 1..4) {
                        drawCircle(
                            color = NeonGreen.copy(alpha = webA * 0.6f),
                            radius = webR * ring / 4f, center = center,
                            style = Stroke(width = 1f),
                        )
                    }
                }
                .graphicsLayer {
                    alpha = enter
                    translationY = (1f - enter) * 36f
                }
                .padding(if (isTv) screenPadding(true) else 0.dp),   // phone эскиз = full-bleed
        ) {
            // ⛔ Particles убраны на ТВ (owner «без анимаций»): 20с infinite-clock + полноэкранная
            // перерисовка каждый кадр — дорого для 1 ГБ-боксов. Атмосферу даёт статичный glow+web ниже.

            if (isTv) {
                // ── LANDSCAPE (TV): logo + spider medallion at the TOP (hero), login + days in a
                // FIXED bar at the BOTTOM of the screen (owner layout). The account bar has its OWN
                // reserved slot below the hero+menu Row, so a tall hero / big medallion on a
                // high-density TV can never push it off-screen (that was the 1.0.125 regression where
                // a weight-spacer inside the hero collapsed to 0 and login+days vanished).
                Column(modifier = Modifier.fillMaxSize()) {
                    Row(
                        modifier = Modifier.fillMaxWidth().weight(1f),
                        verticalAlignment = Alignment.Top,
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
                            modifier = Modifier.weight(0.42f).fillMaxHeight(),
                            showAccountCard = false, // rendered as the fixed bottom bar below instead
                        )
                        Spacer(Modifier.width(28.dp))
                        MenuPane(
                            protocols = protocols,
                            selected = selected,
                            isTv = true,
                            wood = true, // Dark-Fantasy kit на ТВ (бронза/золото/jade, статичный NinePatch)
                            onSelectProtocol = onSelectProtocol,
                            hasOlcrtcCreds = hasOlcrtcCreds,
                            onSelectOlcrtc = onSelectOlcrtc,
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
                    // FIXED bottom bar — login + days, always visible (own slot, never clipped).
                    if (!accountLogin.isNullOrBlank() || daysLeft != null) {
                        Spacer(Modifier.height(10.dp))
                        AccountCard(
                            login = accountLogin,
                            daysLeft = daysLeft,
                            wood = true, // Dark-Fantasy карта аккаунта на ТВ (frame_panel + jade-бейдж)
                            modifier = Modifier
                                .align(Alignment.CenterHorizontally)
                                .widthIn(min = 240.dp, max = 620.dp),
                        )
                    }
                }
            } else {
                // ── PHONE: премиум-эскиз как ФИКСИРОВАННЫЙ фон (резная рамка/дерево/плющ/логотип +
                // медальон-кольцо = пиксели `home_eskiz`, НЕ скроллится), а ЖИВОЙ контент скроллится
                // поверх деревянного окна. Архитектура (owner): рамка+логотип не двигаются, крутится
                // только внутренний контент, где плиток БОЛЬШЕ шести. Наш живой паук — в backdrop-режиме
                // поверх медальона эскиза; статус/аккаунт/полное меню (wood=true) идут ниже, скроллом. ──
                BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
                    // (a) ФИКСИРОВАННЫЙ фон — резная рама/дерево/плющ/логотип/вензели (верх+низ) + пустой
                    //     медальон-обод запечены в `home_backdrop`. НЕ скроллится.
                    Image(
                        painter = painterResource(R.drawable.home_backdrop),
                        contentDescription = null,
                        modifier = Modifier.fillMaxSize(),
                        contentScale = ContentScale.Crop,
                    )
                    // (a2) ПОДКЛЮЧЕНО → в проёме медальона ОТКРЫВАЕТСЯ «глаз» (тот же фон, но с запечённым
                    //      в проём глазом-эталоном owner). Кросс-фейд по connected. ОТКЛЮЧЕНО → чистый
                    //      зелёный диск (обычный home_backdrop, без паука). Глаз запечён В ФОН → проём
                    //      совпадает Crop-ом сам, без ручной посадки.
                    val eyeAlpha by animateFloatAsState(
                        if (connected) 1f else 0f, tween(800, easing = FastOutSlowInEasing), label = "eye",
                    )
                    if (eyeAlpha > 0.004f) {
                        Image(
                            painter = painterResource(R.drawable.home_backdrop_connected),
                            contentDescription = null,
                            modifier = Modifier.fillMaxSize(),
                            contentScale = ContentScale.Crop,
                            alpha = eyeAlpha,
                        )
                    }
                    // Живой отсвет медальона на дереве ПОД кольцом (1:1 к эталону owner):
                    // ОТКЛЮЧЕНО = тёплый красный, ПОДКЛЮЧЕНО = зелёный; кроссфейд вместе с глазом.
                    // Рисуется по геометрии Crop-фона (853×1844, сокет (428,711), обод R≈341).
                    Box(
                        Modifier
                            .fillMaxSize()
                            .drawBehind {
                                val sc = kotlin.math.max(size.width / 853f, size.height / 1844f)
                                val cx = (size.width - 853f * sc) / 2f + 428f * sc
                                val cy = (size.height - 1844f * sc) / 2f + 711f * sc
                                val r = 341f * sc
                                val gy = cy + r * 1.02f
                                fun DrawScope.underGlow(c: Color, a: Float) {
                                    if (a <= 0.01f) return
                                    withTransform({ scale(1f, 0.34f, pivot = Offset(cx, gy)) }) {
                                        drawCircle(
                                            brush = Brush.radialGradient(
                                                listOf(c.copy(alpha = 0.30f * a), Color.Transparent),
                                                center = Offset(cx, gy), radius = r * 1.18f,
                                            ),
                                            radius = r * 1.18f, center = Offset(cx, gy),
                                            blendMode = BlendMode.Plus,
                                        )
                                    }
                                }
                                underGlow(Color(0xFFB23222), 1f - eyeAlpha)
                                underGlow(Color(0xFF2FD16B), eyeAlpha)
                            },
                    )

                    // Геометрия рамы (owner: рама/логотип/вензели/кнопка СТАТИЧНЫ; крутится ТОЛЬКО меню
                    // протоколы→контакты, уходя ВВЕРХ ЗА кнопку и скрываясь за нижним вензелем).
                    val windowTop = maxHeight * 0.585f   // больше воздуха между глазом и статусом (owner) → верх скролл-окна
                    val windowBot = maxHeight * 0.070f   // отступ снизу → нижний вензель рамы остаётся видимым/статичным

                    // (b) СКРОЛЛ-ОКНО меню — ОБРЕЗАНО по [под медальоном .. над нижним вензелем]. Статус/
                    //     аккаунт/меню скроллятся ВНУТРИ окна: сверху исчезают за кнопкой, снизу — за вензелем.
                    Box(
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(top = windowTop, bottom = windowBot)
                            .clipToBounds(),
                    ) {
                        Column(
                            modifier = Modifier
                                .fillMaxSize()
                                .verticalScroll(rememberScrollState())
                                .padding(start = 18.dp, end = 18.dp, bottom = 24.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                        ) {
                            // LIVE статус-строка (точка + текст, активный протокол).
                            PhoneStatusRow(
                                statusText = statusText,
                                connected = connected,
                                activeProtocol = activeProtocol,
                                selected = selected,
                            )
                            // LIVE аккаунт-карточка (логин + дни) — золото/дерево.
                            if (!accountLogin.isNullOrBlank() || daysLeft != null) {
                                Spacer(Modifier.height(14.dp))
                                AccountCard(
                                    login = accountLogin,
                                    daysLeft = daysLeft,
                                    wood = true,
                                    modifier = Modifier.fillMaxWidth().widthIn(max = 460.dp),
                                )
                            }
                            Spacer(Modifier.height(18.dp))
                            // Полное меню (протоколы → покупка → действия → контакты) — единственное, что скроллится.
                            MenuPane(
                                protocols = protocols,
                                selected = selected,
                                isTv = false,
                                onSelectProtocol = onSelectProtocol,
                                hasOlcrtcCreds = hasOlcrtcCreds,
                                onSelectOlcrtc = onSelectOlcrtc,
                                onBuy = onBuy,
                                onEnterCode = onEnterCode,
                                onSplitTunnel = onSplitTunnel,
                                onShareIos = onShareIos,
                                onScanQr = onScanQr,
                                onEnterTrial = onEnterTrial,
                                showTrial = !hasSubProfile,
                                wood = true,
                                modifier = Modifier.fillMaxWidth().widthIn(max = 520.dp),
                            )
                        }
                    }

                    // (c) ФИКСИРОВАННАЯ прозрачная ТАП-ЗОНА на проёме медальона ПОВЕРХ скролла → меню
                    //     уходит ЗА неё. Глаз/диск запечены в фон (проём фиксирован Crop-ом сам); тап-зона
                    //     садится на тот же проём Crop-матрицей (центр обода dcx=433/dcy=693, r=206 = полный проём).
                    val imgW = 853f; val imgH = 1844f
                    // Геометрия НОВОГО (макетного) медальона: стеклянный орб центр (430,711), R≈228
                    val dcx = 430.0f; val dcy = 711.0f; val dr = 228f
                    val medS = maxOf(maxWidth.value / imgW, maxHeight.value / imgH)   // ContentScale.Crop scale
                    val medR = dr * medS
                    val medCx = (maxWidth.value - imgW * medS) / 2f + dcx * medS
                    val medCy = (maxHeight.value - imgH * medS) / 2f + dcy * medS
                    Box(
                        modifier = Modifier
                            .offset(x = (medCx - medR).dp, y = (medCy - medR).dp)
                            .size((2f * medR).dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        // Медальон-слой: ВСЕГДА стеклянный купол (объём диска, не плоский круг);
                        // ПОДКЛЮЧЕНО → живой глаз (дыхание/зрачок/радужка/блик/моргание).
                        MedallionOverlay(
                            connected = connected,
                            eyeAlpha = eyeAlpha,
                            modifier = Modifier.fillMaxSize(),
                        )
                        Button(
                            onClick = onToggleConnect,
                            shape = CircleShape,
                            contentPadding = PaddingValues(0.dp),
                            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
                            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
                            modifier = Modifier.fillMaxSize(),
                            content = {},
                        )
                    }
                }
            }
        }
    }
}

/** Login + days-left glass card (bottom of the hero on both phone and TV). Computes the day
 *  colour/label — green / orange when ≤5 / red when expired / «Безлимит» for unlimited — from [daysLeft]. */
@Composable
private fun AccountCard(login: String?, daysLeft: Int?, modifier: Modifier = Modifier, wood: Boolean = false) {
    val expired = daysLeft != null && daysLeft <= 0
    val low = daysLeft != null && daysLeft in 1..5
    val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else NeonGreen
    val daysText = when {
        daysLeft == null -> null
        expired -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит" // unlimited/owner accounts (~10y+) → don't show an absurd count
        else -> "Осталось $daysLeft ${daysWord(daysLeft)}"
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
private fun PhoneStatusRow(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
) {
    Spacer(Modifier.height(6.dp))
    // 1:1 к эталону owner: ОТКЛЮЧЕНО = красная точка + красный текст (было серебро/белый),
    // и строка протокола видна в ОБОИХ состояниях («Отключён: Vless-s3 • авто»).
    val stateRed = Color(0xFFFF4040)
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
    val protoMain = if (!activeProtocol.isNullOrBlank()) activeProtocol else selected
    if (!protoMain.isNullOrBlank()) {
        Spacer(Modifier.height(8.dp))
        val viaAuto = selected == "auto" && protoMain != "auto"
        val prefix = if (connected) "Подключён" else "Отключён"
        Text(
            if (viaAuto) "$prefix: ${protocolLabel(protoMain)}  •  авто" else "$prefix: ${protocolLabel(protoMain)}",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = MaestroOrange,
        )
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
    showAccountCard: Boolean = true,
) {
    val isTv = rememberIsTv()
    val lowRam = rememberIsLowRam()
    // ⛔ breath-клок ЗАМОРОЖЕН на ТВ (owner «без анимаций», HeroPane — только ТВ): вечный
    // infinite-transition будил choreographer каждый кадр. 0f → лого-гало и гало-точка статуса
    // становятся статичным слабым свечением (Dark-Fantasy атмосфера без движения, 0 fps в покое).
    val breath = 0f
    // wave (световая волна по лого на подключение) убрана — «без анимаций».

    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        // Brand wordmark — the owner's glossy green-glass PANEL. A gentle green glow breathes
        // behind it when connected; on connect a light wave runs ACROSS the panel (SrcAtop → only
        // over the logo pixels, not the transparent margins). Full hero width, one whole piece.
        Image(
            painter = painterResource(R.drawable.maestro_wordmark),
            contentDescription = "MaestroVPN",
            contentScale = ContentScale.Fit,
            modifier = Modifier
                .fillMaxWidth()
                .drawBehind {
                    val pulse = if (connected && !lowRam) breath else 0f
                    val a = 0.04f + 0.10f * pulse
                    if (a > 0.001f) {
                        drawRect(
                            Brush.radialGradient(
                                listOf(NeonGreen.copy(alpha = a), Color.Transparent),
                                center = Offset(size.width / 2f, size.height / 2f),
                                radius = size.maxDimension * 0.6f,
                            ),
                        )
                    }
                },
        )
        // Web strand visually connecting the logo panel to the spider medallion below — a faint
        // silk thread + a small web fan. Static (draw-phase reads only `connected`) → cheap.
        WebConnector(
            connected = connected,
            modifier = Modifier
                .fillMaxWidth()
                .height(if (isTv) 22.dp else 26.dp),
        )

        // ТВ (Dark-Fantasy, БЕЗ анимаций — 1 ГБ-боксы): статичный медальон с вензелем-брендом VM.
        // HeroPane вызывается только в isTv-ветке, поэтому здесь всегда ТВ.
        StaticTvMedallion(
            connected = connected,
            onToggle = onToggleConnect,
            focusRequester = connectFocus,
        )

        Spacer(Modifier.height(14.dp))
        // status with a state dot — the dot gets a soft glow HALO that pulses (breathes) when
        // connected, at the same rate as the spider's eyes.
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier.size(22.dp).drawBehind {
                    if (connected) {
                        val pulse = if (lowRam) 0f else breath
                        val a = 0.30f + 0.42f * pulse
                        val r = size.minDimension / 2f
                        drawCircle(
                            Brush.radialGradient(
                                listOf(NeonGreen.copy(alpha = a), Color.Transparent),
                                center = Offset(size.width / 2f, size.height / 2f),
                                radius = r,
                            ),
                            radius = r,
                        )
                    }
                },
            ) {
                Box(Modifier.size(11.dp).clip(CircleShape).background(if (connected) NeonGreen else MaestroSilver))
            }
            Spacer(Modifier.width(9.dp))
            Text(
                statusText.uppercase(),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = if (connected) NeonGreen else MaterialTheme.colorScheme.onSurface,
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

        // Account (login + days) — PHONE only (TV renders it as a fixed bottom bar in TvHomeScreen so
        // it can never be clipped by a tall hero). Shows «Безлимит» for unlimited accounts.
        if (showAccountCard && (!accountLogin.isNullOrBlank() || daysLeft != null)) {
            Spacer(Modifier.height(12.dp))
            AccountCard(
                login = accountLogin,
                daysLeft = daysLeft,
                modifier = Modifier.widthIn(min = 240.dp),
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
    hasOlcrtcCreds: Boolean,
    onSelectOlcrtc: () -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit,
    onShareIos: () -> Unit,
    onScanQr: () -> Unit,
    onEnterTrial: () -> Unit = {},
    showTrial: Boolean = false,
    wood: Boolean = false,
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

    // Protocol chips + secondary-action tiles: a COMPACT 3-per-row grid on every surface
    // (matches the design mock). Long RU names wrap to 2 lines in NeonChip so they FIT.
    val protoCols = 3

    // olcRTC is a TEASER: shown to EVERYONE, but usable ONLY by owner logins whose
    // creds arrive via /info (hasOlcrtcCreds). Non-owner clients have no olcrtc outbound
    // in their sing-box config, so it appears LOCKED (🔒, dimmed, tap = "по запросу",
    // never selectable) — selecting it would route to a dead 127.0.0.1:8808 and kill
    // their connection. The working entry (owner) comes from the server list as usual.
    val displayProtocols = if (protocols.contains("olcrtc")) protocols else protocols + "olcrtc"

    Column(modifier = modifier) {
        // ── ПРОТОКОЛ — equal-width chrome chips, 3 per row ──
        if (displayProtocols.isNotEmpty()) {
            SectionLabel("ПРОТОКОЛ", wood = wood)
            Spacer(Modifier.height(10.dp))
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                displayProtocols.chunked(protoCols).forEach { row ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        row.forEach { p ->
                            val olcLocked = p == "olcrtc" && !hasOlcrtcCreds
                            NeonChip(
                                label = protocolLabel(p),
                                onClick = { if (olcLocked) onSelectOlcrtc() else onSelectProtocol(p) },
                                modifier = Modifier.weight(1f).heightIn(min = 62.dp),
                                icon = if (olcLocked) Icons.Filled.Lock else protocolIcon(p),
                                selected = p == selected && !olcLocked,
                                subtitle = if (olcLocked) "🔒 по запросу" else protocolBadge(p),
                                locked = olcLocked,
                                wood = wood,
                            )
                        }
                        // keep columns equal-width when the last row is short
                        repeat(protoCols - row.size) { Spacer(Modifier.weight(1f)) }
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
                wood = wood,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(10.dp))
        }

        GlossyButton(
            label = "Купить подписку",
            onClick = onBuy,
            accent = MaestroOrange,
            icon = Icons.Filled.ShoppingCart,
            wood = wood,
            modifier = Modifier.fillMaxWidth(),
        )

        // ── secondary actions — 6 chrome tiles, 3 per row (matches the design mock) ──
        // "Проверить соединение" does a quick 204-probe and toasts the result (no new screen).
        val ctx = LocalContext.current
        val scope = rememberCoroutineScope()
        val onCheckConnection: () -> Unit = {
            scope.launch(Dispatchers.IO) {
                val ok = runCatching {
                    (java.net.URL("https://www.google.com/generate_204").openConnection()
                        as java.net.HttpURLConnection).run {
                        connectTimeout = 5000; readTimeout = 5000
                        try { responseCode in 200..399 } finally { disconnect() }
                    }
                }.getOrDefault(false)
                withContext(Dispatchers.Main) {
                    Toast.makeText(
                        ctx,
                        if (ok) "✅ Соединение работает" else "❌ Нет соединения",
                        Toast.LENGTH_SHORT,
                    ).show()
                }
            }
        }
        Spacer(Modifier.height(12.dp))
        val actionCols = 3
        val tiles = buildList<Triple<String, ImageVector, () -> Unit>> {
            add(Triple("Ввести код", Icons.Filled.Search, onEnterCode))
            if (!isTv) add(Triple("Сканировать QR", Icons.Filled.QrCode2, onScanQr))
            add(Triple("Приложения VPN", Icons.Filled.Public, onSplitTunnel))
            add(Triple("Поделиться", Icons.Filled.Share, onShareIos))
            add(Triple("Обновить приложение", Icons.Filled.CloudDownload, onUpdate))
            add(Triple("Проверить соединение", Icons.Filled.NetworkCheck, onCheckConnection))
        }
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            tiles.chunked(actionCols).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    row.forEach { a ->
                        ChromeTile(a.first, a.second, a.third, Modifier.weight(1f).heightIn(min = 92.dp), wood = wood)
                    }
                    // keep columns equal-width when the last row is short
                    repeat(actionCols - row.size) { Spacer(Modifier.weight(1f)) }
                }
            }
        }

        // ── Контакты ──────────────────────────────────────────────────
        Spacer(Modifier.height(20.dp))
        SectionLabel("КОНТАКТЫ", wood = wood)
        Spacer(Modifier.height(10.dp))
        GlossyButton(
            label = "8 977 811-65-64",
            onClick = { open("tel:+79778116564") },
            accent = NeonGreen,
            icon = Icons.Filled.Call,
            wood = wood,
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
            NeonChip("Telegram", { open("https://t.me/wapmixx") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Send, iconTint = Color(0xFF2AABEE), wood = wood)
            NeonChip("WhatsApp", { open("https://wa.me/79778116564") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Chat, iconTint = Color(0xFF25D366), wood = wood)
            NeonChip("МАКС", { open("https://max.ru/") }, Modifier.weight(1f).heightIn(min = 50.dp), icon = Icons.Filled.Forum, iconTint = Color(0xFF2787F5), wood = wood)
        }
        Spacer(Modifier.height(20.dp))
    }
}

/** Medallion life layer over the glass orb.
 *  Купол/блик/объём теперь ЗАПЕЧЕНЫ в фон из пикселей макета владельца (1:1) — процедурный купол
 *  УДАЛЁН, чтобы не дублировать блик и не «выдумывать» поверх эскиза. Остаётся только едва заметное
 *  зелёное дыхание по ободу в состоянии ПОДКЛЮЧЕНО. Clock runs only while connected (no idle cost). */
@Composable
private fun MedallionOverlay(connected: Boolean, eyeAlpha: Float, modifier: Modifier) {
    val clock = remember { mutableStateOf(0f) }
    LaunchedEffect(connected) {
        if (!connected) return@LaunchedEffect
        val t0 = withFrameNanos { it }
        while (true) {
            withFrameNanos { f -> clock.value = (f - t0) / 1_000_000_000f }
        }
    }
    Box(
        modifier
            .clip(CircleShape)
            .drawBehind {
                val t = clock.value
                val c = center
                val r = size.minDimension / 2f
                // connected: gentle green glow breath — the only life on the crisp eye (no blobs)
                if (eyeAlpha > 0.01f) {
                    val g = 0.06f + 0.05f * sin(t * 1.1f)
                    drawCircle(
                        brush = Brush.radialGradient(
                            0.55f to Color.Transparent,
                            1f to Color(0xFF34E67A).copy(alpha = g * eyeAlpha),
                            center = c, radius = r * 1.06f,
                        ),
                        radius = r * 1.06f, center = c, blendMode = BlendMode.Plus,
                    )
                }
            },
    )
}

/** Small per-protocol glyph for the chips. */
/** Faint silk web connecting the logo panel to the medallion — a thread + a small web fan. Static. */
@Composable
private fun WebConnector(connected: Boolean, modifier: Modifier) {
    Box(
        modifier.drawBehind {
            val cx = size.width / 2f
            val h = size.height
            val a = if (connected) 0.20f else 0.12f
            val col = NeonGreen.copy(alpha = a)
            val colDim = NeonGreen.copy(alpha = a * 0.6f)
            val spread = size.width * 0.13f
            // central silk thread top→bottom
            drawLine(col, Offset(cx, 0f), Offset(cx, h), strokeWidth = 1.6f)
            // a small web fan spreading toward the medallion top
            listOf(-1f, -0.5f, 0.5f, 1f).forEach { k ->
                drawLine(colDim, Offset(cx, 0f), Offset(cx + spread * k, h), strokeWidth = 1f)
            }
            // two cross-strands joining the outer fan strands (web rings)
            for (ring in 1..2) {
                val yf = ring / 3f
                drawLine(colDim, Offset(cx - spread * yf, h * yf), Offset(cx + spread * yf, h * yf), strokeWidth = 0.9f)
            }
        },
    )
}

/** Slow drifting atmosphere particles with 3-depth parallax. Perpetual clock → NON-low-RAM only. */
@Composable
private fun Particles(modifier: Modifier) {
    val t by rememberInfiniteTransition(label = "particles").animateFloat(
        0f, 1f, infiniteRepeatable(tween(20000, easing = LinearEasing), RepeatMode.Restart), label = "ptClock",
    )
    Box(
        modifier.drawBehind {
            for (i in 0 until 18) {
                val fx = frac(i * 0.61803f + 0.13f)
                val depth = i % 3
                val fy = frac(i * 0.7549f + t * (0.5f + depth * 0.35f)) // parallax: deeper drifts faster
                drawCircle(
                    NeonGreen.copy(alpha = 0.05f + 0.045f * depth),
                    radius = 1.2f + depth * 0.9f,
                    center = Offset(fx * size.width, (1f - fy) * size.height),
                )
            }
        },
    )
}

private fun frac(v: Float): Float = v - floor(v)

private fun protocolIcon(tag: String): ImageVector = when (tag) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Bolt
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Hub
    "anytls" -> Icons.Filled.Lock
    "olcrtc" -> Icons.Filled.Videocam
    else -> Icons.Filled.Layers
}

/** Friendly chip label; "auto" is the urltest pick = the lowest-latency protocol. */
private fun protocolLabel(tag: String): String = when (tag) {
    "auto" -> "Авто"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "NaiveProxy"
    "anytls" -> "AnyTLS"
    "olcrtc" -> "olcRTC"
    else -> tag.replaceFirstChar { it.uppercase() }
}

/** Short recommendation badge under each protocol chip (unified style). */
private fun protocolBadge(tag: String): String = when (tag) {
    "auto" -> "Рекомендуется"
    "vless", "vless-s3" -> "Оптимальный"
    "hysteria2" -> "Самый быстрый"
    "naive" -> "⚠ нестабильный"
    "anytls" -> "Без TLS-отпечатка"
    "olcrtc" -> "через Яндекс"
    "trojan" -> "Макс. защита"
    "shadowsocks" -> "Стабильный"
    else -> "Стабильный"
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

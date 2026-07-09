package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NetworkCheck
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.NeonAccountCard
import com.maestrovpn.tv.compose.component.NeonChip
import com.maestrovpn.tv.compose.component.SectionLabel
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * ТВ-главный экран, собранный ПИКСЕЛЬ-В-ПИКСЕЛЬ из эскизов владельца (vpnon/vpnoff).
 *
 * Слои (снизу вверх):
 *  1. Фон `tv_bg_off`→`tv_bg_on` кроссфейдом по [connected] (Crop) — рамка, лого, медальон
 *     (закрытый глаз → открытый), пустое дерево правой панели. Кнопки на фоне ЗАМАЗАНЫ.
 *  2. Медальон-кнопка Connect (слева, невидимая тап-зона на глазу) + фокус-кольцо.
 *  3. Живой статус под медальоном (точка + «ОТКЛЮЧЕНО/ПОДКЛЮЧЕНО» + активный протокол).
 *  4. Правая скролл-колонка: арт-кнопки эскиза (кроп-PNG) в тех же позициях; ниже эскиза
 *     (за нижним краем) — доп-функционал (протоколы / аккаунт / триал / проверка), которого
 *     на эскизе нет, но который нужен приложению — в том же дерево-золото стиле.
 *
 * Всё позиционируется арт-матрицей (арт-space 1672×941 → экран через Crop-scale s), как медальон
 * на телефоне. Раскладку сверяет `ops/tv-eskiz-pipeline.py` (sim совпадает с эскизом 1:1).
 */
@Composable
internal fun TvEskizHome(
    statusText: String,
    connected: Boolean,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    hasSubProfile: Boolean,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit,
    onShareIos: () -> Unit,
    onEnterTrial: () -> Unit,
    connectFocus: FocusRequester,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val open: (String) -> Unit = { url ->
        runCatching {
            ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }
    }
    val onUpdate: () -> Unit = {
        (ctx as? Activity)?.let { act -> scope.launch(Dispatchers.IO) { runCatching { Vendor.checkUpdate(act, true) } } }
    }

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val sw = maxWidth.value
        val sh = maxHeight.value
        val s = maxOf(sw / TvEskizSpec.ART_W, sh / TvEskizSpec.ART_H) // Crop-scale (экран dp на арт-px)
        val offX = (sw - TvEskizSpec.ART_W * s) / 2f
        val offY = (sh - TvEskizSpec.ART_H * s) / 2f
        fun ax(x: Float) = offX + x * s
        fun ay(y: Float) = offY + y * s

        // ── (1) фон off→on кроссфейдом ────────────────────────────────────────────────
        Image(
            painter = painterResource(R.drawable.tv_bg_off),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
            contentScale = ContentScale.Crop,
        )
        val onAlpha by animateFloatAsState(
            if (connected) 1f else 0f, tween(800, easing = FastOutSlowInEasing), label = "tvbg",
        )
        if (onAlpha > 0.001f) {
            Image(
                painter = painterResource(R.drawable.tv_bg_on),
                contentDescription = null,
                modifier = Modifier.fillMaxSize().graphicsLayer { alpha = onAlpha },
                contentScale = ContentScale.Crop,
            )
        }

        // ── (2) медальон-кнопка Connect (невидимая тап-зона на глазу) ─────────────────
        val ringD = 2f * TvEskizSpec.RING_R * s
        MedallionButton(
            connected = connected,
            onToggle = onToggleConnect,
            focusRequester = connectFocus,
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.RING_CX - TvEskizSpec.RING_R).dp, y = ay(TvEskizSpec.RING_CY - TvEskizSpec.RING_R).dp)
                .size(ringD.dp),
        )

        // ── (3) живой статус под медальоном ───────────────────────────────────────────
        EskizStatus(
            statusText = statusText,
            connected = connected,
            activeProtocol = activeProtocol,
            selected = selected,
            dotR = (TvEskizSpec.DOT_R * s).dp,
            modifier = Modifier
                .offset(x = ax(60f).dp, y = ay(TvEskizSpec.STATUS_TOP).dp)
                .width((ax(686f) - ax(60f)).dp),
        )

        // ── (4) правая скролл-панель ──────────────────────────────────────────────────
        Box(
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.PANEL_X0).dp, y = ay(0f).dp)
                .size(width = (TvEskizSpec.PANEL_W * s).dp, height = (TvEskizSpec.ART_H * s).dp)
                .clipToBounds(),
        ) {
            Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Spacer(Modifier.height((TvEskizSpec.TOP_PAD * s).dp))
                ArtBar(TvEskizSpec.BUY, s, onBuy, pill = true, onRes = R.drawable.tv_ek_buy_on, onAlpha = onAlpha)
                ArtRow3(TvEskizSpec.ROW_CODE, s, listOf(onEnterCode, onSplitTunnel, onShareIos))
                ArtBar(TvEskizSpec.UPDATE, s, onUpdate, pill = true)
                ArtBar(TvEskizSpec.KONTAKTY, s, {})
                ArtBar(TvEskizSpec.PHONE, s, { open("tel:+79778116564") }, pill = true)
                ArtBar(TvEskizSpec.HINT, s, {})
                ArtRow3(
                    TvEskizSpec.ROW_TG, s,
                    listOf({ open("https://t.me/wapmixx") }, { open("https://wa.me/79778116564") }, { open("https://max.ru/") }),
                )

                // ── доп-функционал ниже эскиза (единый дерево-золото стиль) ──
                Spacer(Modifier.height(26.dp))
                EskizExtras(
                    protocols = protocols,
                    selected = selected,
                    accountLogin = accountLogin,
                    daysLeft = daysLeft,
                    showTrial = !hasSubProfile,
                    hasOlcrtcCreds = hasOlcrtcCreds,
                    olcrtcProvider = olcrtcProvider,
                    onSelectProtocol = onSelectProtocol,
                    onSelectOlcrtc = onSelectOlcrtc,
                    onEnterTrial = onEnterTrial,
                )
                Spacer(Modifier.height(40.dp))
            }
        }
    }
}

/** Полноширинная арт-кнопка эскиза (кроп-PNG). [onRes]/[onAlpha] — кроссфейд «Купить» off→on. */
@Composable
private fun ArtBar(
    bar: TvEskizSpec.Bar,
    s: Float,
    onClick: () -> Unit,
    pill: Boolean = false,
    onRes: Int? = null,
    onAlpha: Float = 0f,
) {
    val h = (bar.h * s).dp
    if (!bar.focusable) {
        // заголовок/подпись — не фокусируется, просто картинка
        Image(
            painter = painterResource(bar.res),
            contentDescription = null,
            modifier = Modifier.fillMaxWidth().height(h),
            contentScale = ContentScale.FillBounds,
        )
        return
    }
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val scale by animateFloatAsState(if (focused) 1.028f else 1f, tween(140, easing = FastOutSlowInEasing), label = "barScale")
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(h)
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .clickable(interactionSource = interaction, indication = null) { onClick() }
            .focusRing(focused, pill, s),
    ) {
        Image(
            painter = painterResource(bar.res),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
            contentScale = ContentScale.FillBounds,
        )
        if (onRes != null && onAlpha > 0.001f) {
            Image(
                painter = painterResource(onRes),
                contentDescription = null,
                modifier = Modifier.fillMaxSize().graphicsLayer { alpha = onAlpha },
                contentScale = ContentScale.FillBounds,
            )
        }
    }
}

/** Ряд из 3 равных арт-кнопок (код/приложения/поделиться, tg/wa/макс). */
@Composable
private fun ArtRow3(row: TvEskizSpec.Row3, s: Float, onClicks: List<() -> Unit>) {
    val cellW = (TvEskizSpec.CELL_W * s).dp
    val cellH = (row.h * s).dp
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy((TvEskizSpec.GUT * s).dp, Alignment.CenterHorizontally),
    ) {
        row.res.forEachIndexed { i, res ->
            val interaction = remember { MutableInteractionSource() }
            val focused by interaction.collectIsFocusedAsState()
            val scale by animateFloatAsState(if (focused) 1.05f else 1f, tween(140, easing = FastOutSlowInEasing), label = "cellScale")
            Box(
                modifier = Modifier
                    .size(width = cellW, height = cellH)
                    .graphicsLayer { scaleX = scale; scaleY = scale }
                    .clickable(interactionSource = interaction, indication = null) { onClicks.getOrNull(i)?.invoke() }
                    .focusRing(focused, pill = false, s = s),
            ) {
                Image(
                    painter = painterResource(res),
                    contentDescription = null,
                    modifier = Modifier.fillMaxSize(),
                    contentScale = ContentScale.FillBounds,
                )
            }
        }
    }
}

/** Зелёное фокус-кольцо ПОВЕРХ арт-кнопки (внутрь запечённой рамки на ширину кроп-марджина). */
private fun Modifier.focusRing(focused: Boolean, pill: Boolean, s: Float): Modifier = drawWithContent {
    drawContent()
    if (!focused) return@drawWithContent
    val inset = 12f * s
    val stroke = 2.6f * s
    val rad = if (pill) (size.height - 2 * inset) / 2f else 20f * s
    drawRoundRect(
        color = NeonGreen,
        topLeft = Offset(inset, inset),
        size = Size(size.width - 2 * inset, size.height - 2 * inset),
        cornerRadius = CornerRadius(rad, rad),
        style = Stroke(width = stroke),
    )
}

/** Медальон-кнопка: невидимая круглая тап-зона + фокус-кольцо (глаз запечён в фон). */
@Composable
private fun MedallionButton(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val ringColor = if (connected) NeonGreen else MaestroOrange
    Box(
        modifier = modifier
            .focusRequester(focusRequester)
            .clickable(interactionSource = interaction, indication = null) { onToggle() }
            .drawBehind {
                if (focused) {
                    val r = size.minDimension / 2f - 3.dp.toPx()
                    drawCircle(color = ringColor, radius = r, center = center, style = Stroke(width = 3.dp.toPx()))
                }
            },
    )
}

/** Живой статус под медальоном: точка + «ОТКЛЮЧЕНО/ПОДКЛЮЧЕНО» + строка активного протокола. */
@Composable
private fun EskizStatus(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
    dotR: androidx.compose.ui.unit.Dp,
    modifier: Modifier,
) {
    val stateColor = if (connected) NeonGreen else Color(0xFF9C9C9C)
    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(dotR * 2f).drawBehind { drawCircle(stateColor) })
            Spacer(Modifier.width(10.dp))
            Text(
                statusText.uppercase(),
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = stateColor,
                textAlign = TextAlign.Center,
            )
        }
        val protoMain = if (!activeProtocol.isNullOrBlank()) activeProtocol else selected
        if (!protoMain.isNullOrBlank()) {
            Spacer(Modifier.height(8.dp))
            val viaAuto = selected == "auto" && protoMain != "auto"
            val prefix = if (connected) "Подключён" else "Отключён"
            Text(
                if (viaAuto) "$prefix: ${protocolLabel(protoMain)}  •  авто" else "$prefix: ${protocolLabel(protoMain)}",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaestroOrange,
                textAlign = TextAlign.Center,
            )
        }
    }
}

/** Доп-функционал под эскизом: протоколы + аккаунт + триал + проверка соединения (дерево-золото). */
@Composable
private fun EskizExtras(
    protocols: List<String>,
    selected: String?,
    accountLogin: String?,
    daysLeft: Int?,
    showTrial: Boolean,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit,
    onEnterTrial: () -> Unit,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val displayProtocols = if (protocols.contains("olcrtc")) protocols else protocols + "olcrtc"

    Column(Modifier.fillMaxWidth()) {
        if (displayProtocols.isNotEmpty()) {
            SectionLabel("ПРОТОКОЛ", wood = true)
            Spacer(Modifier.height(10.dp))
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                displayProtocols.chunked(3).forEach { rowP ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        rowP.forEach { p ->
                            val olcLocked = p == "olcrtc" && !hasOlcrtcCreds
                            NeonChip(
                                label = protocolLabel(p),
                                onClick = { if (olcLocked) onSelectOlcrtc() else onSelectProtocol(p) },
                                modifier = Modifier.weight(1f).heightIn(min = 62.dp),
                                icon = if (olcLocked) Icons.Filled.Lock else protocolIconExtra(p),
                                selected = p == selected && !olcLocked,
                                subtitle = when {
                                    olcLocked -> "🔒 по запросу"
                                    p == "olcrtc" -> if (olcrtcProvider == "wbstream") "через WB" else "через Яндекс"
                                    else -> null
                                },
                                locked = olcLocked,
                                wood = true,
                            )
                        }
                        repeat(3 - rowP.size) { Spacer(Modifier.weight(1f)) }
                    }
                }
            }
            Spacer(Modifier.height(16.dp))
        }

        if (!accountLogin.isNullOrBlank() || daysLeft != null) {
            val expired = daysLeft != null && daysLeft <= 0
            val low = daysLeft != null && daysLeft in 1..5
            val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else NeonGreen
            val daysText = when {
                daysLeft == null -> null
                expired -> "Подписка истекла"
                daysLeft >= 3650 -> "Безлимит"
                else -> "Осталось $daysLeft ${daysWord(daysLeft)}"
            }
            NeonAccountCard(
                login = accountLogin,
                daysText = daysText,
                daysColor = daysColor,
                leadingIcon = Icons.Filled.Person,
                trailingIcon = Icons.Filled.CalendarMonth,
                wood = true,
                modifier = Modifier.fillMaxWidth().widthIn(max = 620.dp),
            )
            Spacer(Modifier.height(12.dp))
        }

        if (showTrial) {
            com.maestrovpn.tv.compose.component.GlossyButton(
                label = "🎁 Попробовать 2 дня бесплатно",
                onClick = onEnterTrial,
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(10.dp))
        }

        val onCheck: () -> Unit = {
            scope.launch(Dispatchers.IO) {
                val ok = runCatching {
                    (java.net.URL("https://www.google.com/generate_204").openConnection() as java.net.HttpURLConnection).run {
                        connectTimeout = 5000; readTimeout = 5000
                        try { responseCode in 200..399 } finally { disconnect() }
                    }
                }.getOrDefault(false)
                withContext(Dispatchers.Main) {
                    Toast.makeText(ctx, if (ok) "✅ Соединение работает" else "❌ Нет соединения", Toast.LENGTH_SHORT).show()
                }
            }
        }
        com.maestrovpn.tv.compose.component.ChromeTile(
            "Проверить соединение", Icons.Filled.NetworkCheck, onCheck,
            Modifier.fillMaxWidth().heightIn(min = 72.dp), wood = true,
        )
    }
}

private fun protocolIconExtra(tag: String): ImageVector = when (tag) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Bolt
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Hub
    "anytls" -> Icons.Filled.Lock
    "olcrtc" -> Icons.Filled.Videocam
    else -> Icons.Filled.Layers
}

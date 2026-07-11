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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NetworkCheck
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
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
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
    accountExpires: String?,
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
        ArtImage(R.drawable.tv_bg_off, Modifier.fillMaxSize(), ContentScale.Crop)
        val onAlpha by animateFloatAsState(
            if (connected) 1f else 0f, tween(800, easing = FastOutSlowInEasing), label = "tvbg",
        )
        if (onAlpha > 0.001f) {
            ArtImage(R.drawable.tv_bg_on, Modifier.fillMaxSize(), ContentScale.Crop, alpha = onAlpha)
        }

        // ── (2) медальон-кнопка Connect (невидимая тап-зона на глазу) ─────────────────
        val ringD = 2f * TvEskizSpec.RING_R * s
        MedallionButton(
            connected = connected,
            onToggle = onToggleConnect,
            focusRequester = connectFocus,
            s = s,
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.RING_CX - TvEskizSpec.RING_R).dp, y = ay(TvEskizSpec.RING_CY - TvEskizSpec.RING_R).dp)
                .size(ringD.dp),
        )

        // ── (3) живой статус под медальоном (+ строка аккаунта: логин · дни · до даты) ──
        // Контейнер 788..936 арт-y: OFF — верхний якорь (позиция эскиза), ON — нижний
        // (кольцо ON толще, штрих до 830 → компактный блок у низа его не касается).
        // Низ дополнительно ограничен краем экрана, чтобы текст не резался НИКОГДА.
        val statusTopDp = ay(TvEskizSpec.STATUS_TOP)
        val statusBottomDp = minOf(ay(TvEskizSpec.STATUS_BOTTOM), sh - 3f)
        Box(
            modifier = Modifier
                .offset(x = ax(60f).dp, y = statusTopDp.dp)
                .size(width = (ax(686f) - ax(60f)).dp, height = (statusBottomDp - statusTopDp).dp),
            contentAlignment = if (connected) Alignment.BottomCenter else Alignment.TopCenter,
        ) {
            EskizStatus(
                statusText = statusText,
                connected = connected,
                activeProtocol = activeProtocol,
                selected = selected,
                accountLogin = accountLogin,
                daysLeft = daysLeft,
                accountExpires = accountExpires,
                dotR = (TvEskizSpec.DOT_R * s).dp,
                modifier = Modifier.fillMaxWidth(),
            )
        }

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
                // Арт-зона: кропы на АБСОЛЮТНЫХ позициях эскиза (z-порядок = порядок вызовов;
                // телефон легально перекрывает фезер-поле КОНТАКТОВ — так в самом арте).
                Box(Modifier.fillMaxWidth().height((TvEskizSpec.ZONE_H * s).dp)) {
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
                }

                // ── доп-функционал ниже эскиза (единый дерево-золото стиль) ──
                EskizExtras(
                    protocols = protocols,
                    selected = selected,
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

/** Полноширинная арт-кнопка эскиза (кроп-PNG) на АБСОЛЮТНОЙ позиции bar.top (арт-y).
 *  [onRes]/[onAlpha] — кроссфейд «Купить» off→on. */
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
    val place = Modifier
        .offset(y = (bar.top * s).dp)
        .fillMaxWidth()
        .height(h)
    if (!bar.focusable) {
        // заголовок/подпись — не фокусируется, просто картинка
        ArtImage(bar.res, place, ContentScale.FillBounds)
        return
    }
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    // ⛔ БЕЗ scale на фокусе: кропы с фезер-полями вклеены в запечённый фон пиксель-в-пиксель,
    // любое масштабирование двоит края (артефакты). Фокус = только чёткое кольцо.
    Box(
        modifier = place
            .clickable(interactionSource = interaction, indication = null) { onClick() }
            .focusRing(focused, pill, s),
    ) {
        ArtImage(bar.res, Modifier.fillMaxSize(), ContentScale.FillBounds)
        if (onRes != null && onAlpha > 0.001f) {
            ArtImage(onRes, Modifier.fillMaxSize(), ContentScale.FillBounds, alpha = onAlpha)
        }
    }
}

/** Ряд из 3 равных арт-кнопок (код/приложения/поделиться, tg/wa/макс) на абсолютных позициях. */
@Composable
private fun ArtRow3(row: TvEskizSpec.Row3, s: Float, onClicks: List<() -> Unit>) {
    val cellW = (TvEskizSpec.CELL_W * s).dp
    val cellH = (row.h * s).dp
    row.res.forEachIndexed { i, res ->
        val cellX = (TvEskizSpec.COL_X0 - TvEskizSpec.PANEL_X0 + i * (TvEskizSpec.CELL_W + TvEskizSpec.GUT)) * s
        val interaction = remember { MutableInteractionSource() }
        val focused by interaction.collectIsFocusedAsState()
        // без scale (см. ArtBar) — фокус только кольцом, края кропов не двоятся
        Box(
            modifier = Modifier
                .offset(x = cellX.dp, y = (row.top * s).dp)
                .size(width = cellW, height = cellH)
                .clickable(interactionSource = interaction, indication = null) { onClicks.getOrNull(i)?.invoke() }
                .focusRing(focused, pill = false, s = s),
        ) {
            ArtImage(res, Modifier.fillMaxSize(), ContentScale.FillBounds)
        }
    }
}

/** Битмап-картинка с High-фильтрацией: арт масштабируется на экран (и вверх на 4K),
 *  дефолтный Low-фильтр Compose мылит текстуру дерева/металла. */
@Composable
private fun ArtImage(res: Int, modifier: Modifier, contentScale: ContentScale, alpha: Float = 1f) {
    Image(
        bitmap = ImageBitmap.imageResource(res),
        contentDescription = null,
        modifier = modifier,
        contentScale = contentScale,
        alpha = alpha,
        filterQuality = FilterQuality.High,
    )
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
    s: Float,
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
                    // обводка по ВИДИМОМУ кольцу текущего состояния (чуть внутрь штриха),
                    // а не по общей тап-зоне — иначе в ON она висела в воздухе и цепляла статус
                    val px = s.dp.toPx() // px на один арт-пиксель
                    val cxArt = if (connected) TvEskizSpec.RING_ON_CX else TvEskizSpec.RING_OFF_CX
                    val cyArt = if (connected) TvEskizSpec.RING_ON_CY else TvEskizSpec.RING_OFF_CY
                    val rArt = if (connected) TvEskizSpec.RING_ON_RVIS else TvEskizSpec.RING_OFF_RVIS
                    val boxArtX = TvEskizSpec.RING_CX - TvEskizSpec.RING_R
                    val boxArtY = TvEskizSpec.RING_CY - TvEskizSpec.RING_R
                    drawCircle(
                        color = ringColor,
                        radius = rArt * px,
                        center = Offset((cxArt - boxArtX) * px, (cyArt - boxArtY) * px),
                        style = Stroke(width = 3.dp.toPx()),
                    )
                }
            },
    )
}

/** Живой статус под медальоном: точка + «ОТКЛЮЧЕНО/ПОДКЛЮЧЕНО» + строка активного протокола
 *  + строка аккаунта (owner: логин, сколько дней и дата окончания подписки — видны сразу). */
@Composable
private fun EskizStatus(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    dotR: androidx.compose.ui.unit.Dp,
    modifier: Modifier,
) {
    val stateColor = if (connected) NeonGreen else Color(0xFF9C9C9C)
    // Компактные межстрочные (24/17/15) + зазоры 2dp: весь блок ≤ 60dp и гарантированно
    // помещается в контейнер 788..936 в ОБОИХ состояниях (размеры шрифтов не тронуты).
    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(dotR * 2f).drawBehind { drawCircle(stateColor) })
            Spacer(Modifier.width(10.dp))
            Text(
                statusText.uppercase(),
                style = MaterialTheme.typography.titleLarge.copy(lineHeight = 24.sp),
                fontWeight = FontWeight.Bold,
                color = stateColor,
                textAlign = TextAlign.Center,
            )
        }
        val protoMain = if (!activeProtocol.isNullOrBlank()) activeProtocol else selected
        if (!protoMain.isNullOrBlank()) {
            Spacer(Modifier.height(2.dp))
            val viaAuto = selected == "auto" && protoMain != "auto"
            val prefix = if (connected) "Подключён" else "Отключён"
            Text(
                if (viaAuto) "$prefix: ${protocolLabel(protoMain)}  •  авто" else "$prefix: ${protocolLabel(protoMain)}",
                style = MaterialTheme.typography.titleMedium.copy(lineHeight = 17.sp),
                fontWeight = FontWeight.Bold,
                color = MaestroOrange,
                textAlign = TextAlign.Center,
            )
        }
        // Аккаунт: «login · осталось N дней · до 02.08.2026» (истёк → красным, безлимит → без даты)
        val expired = daysLeft != null && daysLeft <= 0
        val accountLine = buildList {
            accountLogin?.takeIf { it.isNotBlank() }?.let { add(it) }
            when {
                daysLeft == null -> {}
                expired -> add("подписка истекла")
                daysLeft >= 3650 -> add("безлимит")
                else -> {
                    add("осталось $daysLeft ${daysWord(daysLeft)}")
                    accountExpires?.let { add("до $it") }
                }
            }
        }.joinToString(" · ")
        if (accountLine.isNotBlank()) {
            Spacer(Modifier.height(2.dp))
            Text(
                accountLine,
                style = MaterialTheme.typography.titleSmall.copy(lineHeight = 15.sp),
                fontWeight = FontWeight.Bold,
                color = if (expired) Color(0xFFE5484D) else Color(0xFFE8C877), // GoldHi кита
                textAlign = TextAlign.Center,
            )
        }
    }
}

/** Доп-функционал под эскизом: протоколы + триал + проверка соединения (дерево-золото).
 *  Аккаунт здесь НЕ дублируется — он всегда виден строкой под медальоном (owner). */
@Composable
private fun EskizExtras(
    protocols: List<String>,
    selected: String?,
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

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
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NetworkCheck
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
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

        // ── (1.5) premium лого-панель (рама эталона + родная надпись из эскиза) ──────
        // Накрывает старую тонкую фигурную раму фона целиком (в ассет запечена и
        // деревянная заплатка под нижним зелёным свечением старой рамы, до верха кольца).
        ArtImage(
            R.drawable.tvp_logo,
            Modifier
                .offset(x = ax(84f).dp, y = ay(16f).dp)
                .size(width = (584f * s).dp, height = (330f * s).dp),
            ContentScale.FillBounds,
        )

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
                // Premium-зона v3: панели из материала эталона (ops/tv-premium-kit.py) на тех же
                // АБСОЛЮТНЫХ позициях эскиза; контент (иконка+текст) и янтарный фокус — Compose.
                Box(Modifier.fillMaxWidth().height((TvEskizSpec.ZONE_H * s).dp)) {
                    PremiumBar(
                        TvEskizSpec.BUY, s, onBuy, pill = true,
                        icon = Icons.Filled.ShoppingCart, label = "Купить подписку",
                        labelColor = SaladText, fontArtPx = 34f,
                    )
                    PremiumRow3(
                        TvEskizSpec.ROW_CODE, s,
                        listOf(
                            Triple(Icons.Filled.Search, "Ввести код", onEnterCode),
                            Triple(Icons.Filled.Public, "Приложения VPN", onSplitTunnel),
                            Triple(Icons.Filled.Share, "Поделиться", onShareIos),
                        ),
                        iconAbove = true,
                    )
                    PremiumBar(
                        TvEskizSpec.UPDATE, s, onUpdate, pill = true,
                        icon = Icons.Filled.CloudDownload, label = "Обновить приложение",
                        labelColor = IvoryText, fontArtPx = 30f,
                    )
                    SectionHeadline(TvEskizSpec.KONTAKTY, s, "КОНТАКТЫ")
                    PremiumBar(
                        TvEskizSpec.PHONE, s, { open("tel:+79778116564") }, pill = true,
                        icon = Icons.Filled.Call, label = "8 977 811-65-64",
                        labelColor = SaladText, fontArtPx = 40f,
                    )
                    HintLine(TvEskizSpec.HINT, s, "Если я не ответил на звонок — обязательно напишите в любом из мессенджеров 👇")
                    PremiumRow3(
                        TvEskizSpec.ROW_TG, s,
                        listOf(
                            Triple(Icons.Filled.Send, "Telegram", { open("https://t.me/wapmixx") }),
                            Triple(Icons.Filled.Chat, "WhatsApp", { open("https://wa.me/79778116564") }),
                            Triple(Icons.Filled.Forum, "МАКС", { open("https://max.ru/") }),
                        ),
                        iconAbove = false,
                        iconTints = listOf(Color(0xFF2AABEE), Color(0xFF25D366), Color(0xFF2787F5)),
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

// Палитра premium-панелей: салатовый текст CTA/телефона и слоновая кость (пипетки эскиза).
private val SaladText = Color(0xFFCDE37B)
private val IvoryText = Color(0xFFEDEDE9)
private val FocusGoldTv = Color(0xFFFFCE8C)
private val FocusAmberTv = Color(0xFFFFAD5C)

/** Полноширинная premium-кнопка (панель эталона + иконка + текст) на позиции bar.top (арт-y).
 *  Фокус — единый янтарно-золотой контур по рамке панели (внутрь поля M), БЕЗ scale/blur. */
@Composable
private fun PremiumBar(
    bar: TvEskizSpec.Bar,
    s: Float,
    onClick: () -> Unit,
    icon: ImageVector,
    label: String,
    labelColor: Color,
    fontArtPx: Float,
    pill: Boolean = false,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    Box(
        modifier = Modifier
            .offset(y = (bar.top * s).dp)
            .fillMaxWidth()
            .height((bar.h * s).dp)
            .clickable(interactionSource = interaction, indication = null) { onClick() }
            .focusGold(focused, pill, s),
        contentAlignment = Alignment.Center,
    ) {
        ArtImage(bar.res, Modifier.fillMaxSize(), ContentScale.FillBounds)
        Row(verticalAlignment = Alignment.CenterVertically) {
            androidx.compose.material3.Icon(
                icon, contentDescription = null, tint = labelColor,
                modifier = Modifier.size((fontArtPx * 1.05f * s).dp),
            )
            Spacer(Modifier.width((14f * s).dp))
            Text(
                label,
                color = labelColor,
                fontWeight = FontWeight.Bold,
                fontSize = (fontArtPx * s).sp,
                maxLines = 1,
            )
        }
    }
}

/** Ряд из 3 равных premium-плит (тайлы действий / чипы мессенджеров) на абсолютных позициях. */
@Composable
private fun PremiumRow3(
    row: TvEskizSpec.Row3,
    s: Float,
    items: List<Triple<ImageVector, String, () -> Unit>>,
    iconAbove: Boolean,
    iconTints: List<Color>? = null,
) {
    val cellW = (TvEskizSpec.CELL_W * s).dp
    val cellH = (row.h * s).dp
    items.forEachIndexed { i, (icon, label, click) ->
        val cellX = (TvEskizSpec.COL_X0 - TvEskizSpec.PANEL_X0 + i * (TvEskizSpec.CELL_W + TvEskizSpec.GUT)) * s
        val interaction = remember { MutableInteractionSource() }
        val focused by interaction.collectIsFocusedAsState()
        val tint = iconTints?.getOrNull(i) ?: Color(0xFF6FD65A)
        Box(
            modifier = Modifier
                .offset(x = cellX.dp, y = (row.top * s).dp)
                .size(width = cellW, height = cellH)
                .clickable(interactionSource = interaction, indication = null) { click() }
                .focusGold(focused, pill = false, s = s),
            contentAlignment = Alignment.Center,
        ) {
            ArtImage(row.res[i], Modifier.fillMaxSize(), ContentScale.FillBounds)
            if (iconAbove) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    androidx.compose.material3.Icon(
                        icon, contentDescription = null, tint = tint,
                        modifier = Modifier.size((40f * s).dp),
                    )
                    Spacer(Modifier.height((12f * s).dp))
                    Text(label, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (23f * s).sp, maxLines = 1)
                }
            } else {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    androidx.compose.material3.Icon(
                        icon, contentDescription = null, tint = tint,
                        modifier = Modifier.size((30f * s).dp),
                    )
                    Spacer(Modifier.width((12f * s).dp))
                    Text(label, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (24f * s).sp, maxLines = 1)
                }
            }
        }
    }
}

/** Заголовок секции: Playfair-золото слева + бронзовый орнамент-разделитель до правого края. */
@Composable
private fun SectionHeadline(bar: TvEskizSpec.Bar, s: Float, title: String) {
    Row(
        modifier = Modifier
            .offset(y = (bar.top * s).dp)
            .fillMaxWidth()
            .height((bar.h * s).dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            title,
            color = com.maestrovpn.tv.compose.theme.GoldHi,
            fontFamily = com.maestrovpn.tv.compose.theme.PlayfairFamily,
            fontWeight = FontWeight.Bold,
            fontSize = (34f * s).sp,
            letterSpacing = 3.sp,
            modifier = Modifier.padding(start = (6f * s).dp),
        )
        Spacer(Modifier.width((22f * s).dp))
        ArtImage(
            R.drawable.tvp_divider,
            Modifier.weight(1f).height((24f * s).dp),
            ContentScale.FillBounds,
        )
    }
}

/** Строка-подсказка между телефоном и мессенджерами (нативный текст вместо мыльного кропа). */
@Composable
private fun HintLine(bar: TvEskizSpec.Bar, s: Float, text: String) {
    Box(
        modifier = Modifier
            .offset(y = (bar.top * s).dp)
            .fillMaxWidth()
            .height((bar.h * s).dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text,
            color = Color(0xFFCBCDD2),
            fontSize = (17f * s).sp,
            textAlign = TextAlign.Center,
            maxLines = 2,
        )
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

/** Единый ТВ-фокус: янтарно-золотой двойной контур по рамке панели (внутрь поля M канвы).
 *  Тёплый широкий обод + чёткая золотая линия — сочетается с бронзой, БЕЗ blur/scale. */
private fun Modifier.focusGold(focused: Boolean, pill: Boolean, s: Float): Modifier = drawWithContent {
    drawContent()
    if (!focused) return@drawWithContent
    val inset = 11f * s
    val rad = if (pill) (size.height - 2 * inset) / 2f else 24f * s
    // тёплый полупрозрачный обод
    drawRoundRect(
        color = FocusAmberTv.copy(alpha = 0.55f),
        topLeft = Offset(inset - 2.5f * s, inset - 2.5f * s),
        size = Size(size.width - 2 * (inset - 2.5f * s), size.height - 2 * (inset - 2.5f * s)),
        cornerRadius = CornerRadius(rad + 2.5f * s, rad + 2.5f * s),
        style = Stroke(width = 5f * s),
    )
    // чёткая золотая линия
    drawRoundRect(
        color = FocusGoldTv,
        topLeft = Offset(inset, inset),
        size = Size(size.width - 2 * inset, size.height - 2 * inset),
        cornerRadius = CornerRadius(rad, rad),
        style = Stroke(width = 2.6f * s),
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
    // единый янтарный фокус ТВ (было зелёное/оранжевое кольцо — спорило с бронзовой темой)
    val ringColor = FocusGoldTv
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

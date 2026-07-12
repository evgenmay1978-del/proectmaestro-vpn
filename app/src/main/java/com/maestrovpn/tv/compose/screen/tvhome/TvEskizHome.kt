package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.filled.Wifi
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * ТВ-главный v5 «master quality» (owner 2026-07-12, мок одобрен):
 * один экран 1920×1080 БЕЗ скролла, вся графика — родные пиксели телефонного арта
 * (`ops/tv-mobile-kit.py`). Слои:
 *  1. Фон `tvm_bg_off`→`tvm_bg_on` кроссфейд (дерево+рама+герой: сфера ↔ глаз).
 *  2. Медальон-кнопка Connect (тап-зона на сфере/глазу) + янтарное фокус-кольцо.
 *  3. Статус (красный/зелёный, как на телефоне) + аккаунт-бар (или триал-CTA) слева.
 *  4. Правая зона: CTA+шестерёнка / 3 плитки / обновить+проверить / КОНТАКТЫ /
 *     ПРОТОКОЛ (чипы с подзаголовками, выбранный — оранжевый, как в видео owner).
 * Панели — ассеты с запечёнными тенью/объёмом на АБСОЛЮТНЫХ позициях [TvEskizSpec];
 * контент (иконки/текст) и фокус — Compose. Никаких вечных анимаций (1ГБ-боксы).
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
    onSettings: () -> Unit,
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

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val sw = maxWidth.value
        val sh = maxHeight.value
        val s = maxOf(sw / TvEskizSpec.ART_W, sh / TvEskizSpec.ART_H) // Crop-scale (dp на арт-px)
        val offX = (sw - TvEskizSpec.ART_W * s) / 2f
        val offY = (sh - TvEskizSpec.ART_H * s) / 2f
        fun ax(x: Float) = offX + x * s
        fun ay(y: Float) = offY + y * s

        // ── (1) фон: дерево+рама+герой, off→on кроссфейдом (сфера → глаз) ─────────────
        ArtImage(R.drawable.tvm_bg_off, Modifier.fillMaxSize(), ContentScale.Crop)
        val onAlpha by animateFloatAsState(
            if (connected) 1f else 0f, tween(800, easing = FastOutSlowInEasing), label = "tvbg",
        )
        if (onAlpha > 0.001f) {
            ArtImage(R.drawable.tvm_bg_on, Modifier.fillMaxSize(), ContentScale.Crop, alpha = onAlpha)
        }

        // ── (2) медальон-кнопка Connect ───────────────────────────────────────────────
        MedallionButton(
            onToggle = onToggleConnect,
            focusRequester = connectFocus,
            s = s,
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.RING_CX - TvEskizSpec.RING_R).dp, y = ay(TvEskizSpec.RING_CY - TvEskizSpec.RING_R).dp)
                .size((2f * TvEskizSpec.RING_R * s).dp),
        )

        // ── (3) статус (красный/зелёный, как на телефоне) + подстрока протокола ──────
        val stateColor = if (connected) NeonGreen else StateRed
        Box(
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.HERO_X).dp, y = ay(TvEskizSpec.STATUS_MAIN_Y - 28f).dp)
                .size(width = (TvEskizSpec.HERO_W * s).dp, height = (56f * s).dp),
            contentAlignment = Alignment.Center,
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size((2f * TvEskizSpec.DOT_R * s).dp)
                        .drawBehind { drawCircle(stateColor) },
                )
                Spacer(Modifier.width((14f * s).dp))
                Text(
                    statusText.uppercase(),
                    color = stateColor,
                    fontWeight = FontWeight.Bold,
                    fontSize = (40f * s).sp,
                    letterSpacing = 2.sp,
                    maxLines = 1,
                )
            }
        }
        val protoMain = if (!activeProtocol.isNullOrBlank()) activeProtocol else selected
        if (!protoMain.isNullOrBlank()) {
            val viaAuto = selected == "auto" && protoMain != "auto"
            val prefix = if (connected) "Подключён" else "Отключён"
            Box(
                modifier = Modifier
                    .offset(x = ax(TvEskizSpec.HERO_X).dp, y = ay(TvEskizSpec.STATUS_SUB_Y - 20f).dp)
                    .size(width = (TvEskizSpec.HERO_W * s).dp, height = (40f * s).dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    if (viaAuto) "$prefix: ${protocolLabel(protoMain)}  •  авто" else "$prefix: ${protocolLabel(protoMain)}",
                    color = MaestroOrange,
                    fontWeight = FontWeight.Bold,
                    fontSize = (26f * s).sp,
                    maxLines = 1,
                )
            }
        }

        // ── (3.5) аккаунт-бар / триал-CTA (слот под статусом) ─────────────────────────
        if (!hasSubProfile) {
            PanelBox(TvEskizSpec.TRIAL, s, ::ax, ::ay, onClick = onEnterTrial) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Icon(Icons.Filled.ShoppingCart, null, tint = NeonIcon, modifier = Modifier.size((44f * s).dp))
                    Spacer(Modifier.height((10f * s).dp))
                    Text(
                        "Попробовать 2 дня бесплатно",
                        color = SaladText, fontWeight = FontWeight.Bold, fontSize = (28f * s).sp, maxLines = 1,
                    )
                }
            }
        } else if (!accountLogin.isNullOrBlank() || daysLeft != null) {
            PanelBox(TvEskizSpec.ACCOUNT, s, ::ax, ::ay, onClick = null) {
                AccountBarContent(accountLogin, daysLeft, accountExpires, s)
            }
        }

        // ── (4) правая зона ───────────────────────────────────────────────────────────
        PanelBox(TvEskizSpec.CTA, s, ::ax, ::ay, onClick = onBuy, radArt = 40f) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Filled.ShoppingCart, null, tint = SaladText, modifier = Modifier.size((50f * s).dp))
                Spacer(Modifier.width((22f * s).dp))
                Text("Купить подписку", color = SaladText, fontWeight = FontWeight.Bold, fontSize = (34f * s).sp, maxLines = 1)
            }
        }
        PanelBox(TvEskizSpec.GEAR, s, ::ax, ::ay, onClick = onSettings) {
            Icon(Icons.Filled.Settings, "Настройки", tint = NeonIcon, modifier = Modifier.size((48f * s).dp))
        }

        val tiles = listOf(
            Triple(Icons.Filled.Search, "Ввести код", onEnterCode),
            Triple(Icons.Filled.Public, "Приложения VPN", onSplitTunnel),
            Triple(Icons.Filled.Share, "Поделиться", onShareIos),
        )
        tiles.forEachIndexed { i, (icon, label, click) ->
            PanelBox(
                TvEskizSpec.P(TvEskizSpec.TILE_RES, TvEskizSpec.X0 + i * TvEskizSpec.TILE_STEP, TvEskizSpec.TILE_Y, TvEskizSpec.TILE_W, TvEskizSpec.TILE_H),
                s, ::ax, ::ay, onClick = click,
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Icon(icon, null, tint = NeonIcon, modifier = Modifier.size((48f * s).dp))
                    Spacer(Modifier.height((14f * s).dp))
                    Text(label, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (28f * s).sp, maxLines = 1)
                }
            }
        }

        listOf(
            Triple(Icons.Filled.CloudDownload, "Обновить приложение", onUpdate) to TvEskizSpec.X0,
            Triple(Icons.Filled.Wifi, "Проверить соединение", onCheck) to TvEskizSpec.BAR2_X2,
        ).forEach { (t, x) ->
            val (icon, label, click) = t
            PanelBox(TvEskizSpec.P(TvEskizSpec.BAR2_RES, x, TvEskizSpec.BAR2_Y, TvEskizSpec.BAR2_W, TvEskizSpec.BAR2_H), s, ::ax, ::ay, onClick = click) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(icon, null, tint = NeonIcon, modifier = Modifier.size((44f * s).dp))
                    Spacer(Modifier.width((16f * s).dp))
                    Text(label, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (26f * s).sp, maxLines = 1)
                }
            }
        }

        SectionHeader("КОНТАКТЫ", TvEskizSpec.HDR_CONTACTS_Y, s, ::ax, ::ay)
        PanelBox(TvEskizSpec.PHONE, s, ::ax, ::ay, onClick = { open("tel:+79778116564") }, radArt = 36f) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Filled.Call, null, tint = NeonIcon, modifier = Modifier.size((46f * s).dp))
                Spacer(Modifier.width((22f * s).dp))
                Text("8 977 811-65-64", color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (40f * s).sp, letterSpacing = 1.sp, maxLines = 1)
            }
        }
        Box(
            modifier = Modifier
                .offset(x = ax(TvEskizSpec.X0).dp, y = ay(TvEskizSpec.HINT_Y - 20f).dp)
                .size(width = ((TvEskizSpec.X1 - TvEskizSpec.X0) * s).dp, height = (40f * s).dp),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                "Если я не ответил на звонок — напишите в любом из мессенджеров",
                color = Color(0xFFCBCDD2), fontSize = (22f * s).sp, textAlign = TextAlign.Center, maxLines = 1,
            )
        }
        val msgs = listOf(
            Triple(Icons.Filled.Send, "Telegram", { open("https://t.me/wapmixx") }) to Color(0xFF2AABEE),
            Triple(Icons.Filled.Chat, "WhatsApp", { open("https://wa.me/79778116564") }) to Color(0xFF25D366),
            Triple(Icons.Filled.Forum, "МАКС", { open("https://max.ru/") }) to Color(0xFF2787F5),
        )
        msgs.forEachIndexed { i, (t, tint) ->
            val (icon, label, click) = t
            PanelBox(
                TvEskizSpec.P(TvEskizSpec.MSG_RES, TvEskizSpec.X0 + i * TvEskizSpec.TILE_STEP, TvEskizSpec.MSG_Y, TvEskizSpec.TILE_W, TvEskizSpec.MSG_H),
                s, ::ax, ::ay, onClick = click,
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(icon, null, tint = tint, modifier = Modifier.size((38f * s).dp))
                    Spacer(Modifier.width((16f * s).dp))
                    Text(label, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (28f * s).sp, maxLines = 1)
                }
            }
        }

        SectionHeader("ПРОТОКОЛ", TvEskizSpec.HDR_PROTO_Y, s, ::ax, ::ay)
        val displayProtocols = (if (protocols.contains("olcrtc")) protocols else protocols + "olcrtc").take(8)
        displayProtocols.forEachIndexed { i, p ->
            val r = i / TvEskizSpec.CHIP_COLS
            val c = i % TvEskizSpec.CHIP_COLS
            val olcLocked = p == "olcrtc" && !hasOlcrtcCreds
            val sel = p == selected && !olcLocked
            PanelBox(
                TvEskizSpec.P(
                    if (sel) TvEskizSpec.CHIP_SEL_RES else TvEskizSpec.CHIP_RES,
                    TvEskizSpec.X0 + c * TvEskizSpec.CHIP_STEP_X,
                    TvEskizSpec.CHIP_Y + r * TvEskizSpec.CHIP_STEP_Y,
                    TvEskizSpec.CHIP_W, TvEskizSpec.CHIP_H,
                ),
                s, ::ax, ::ay,
                onClick = { if (olcLocked) onSelectOlcrtc() else onSelectProtocol(p) },
                radArt = 16f,
                center = false,
            ) {
                val titleC = if (sel) MaestroOrange else IvoryText
                val iconC = if (olcLocked) Color(0xFF8F9288) else if (sel) MaestroOrange else NeonIcon
                val badgeC = if (sel) MaestroOrange else BadgeGreen
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxSize().padding(start = (18f * s).dp)) {
                    Icon(
                        if (olcLocked) Icons.Filled.Lock else protocolIconTv(p), null,
                        tint = iconC, modifier = Modifier.size((34f * s).dp),
                    )
                    Spacer(Modifier.width((12f * s).dp))
                    Column {
                        Text(
                            protocolLabel(p), color = titleC, fontWeight = FontWeight.Bold,
                            fontSize = (24f * s).sp, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            when {
                                olcLocked -> "🔒 по запросу"
                                p == "olcrtc" -> if (olcrtcProvider == "wbstream") "через WB" else "через Яндекс"
                                else -> protocolBadge(p)
                            },
                            color = badgeC, fontWeight = FontWeight.Bold,
                            fontSize = (15f * s).sp, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}

// Палитра v4 (видео owner: неон-иконки телефона, красный OFF как на телефоне)
private val SaladText = Color(0xFFCDE37B)
private val IvoryText = Color(0xFFEDEDE9)
private val NeonIcon = Color(0xFF3EE07A)
private val StateRed = Color(0xFFFF4040)
private val BadgeGreen = Color(0xFF96D896)
private val FocusGoldTv = Color(0xFFFFCE8C)
private val FocusAmberTv = Color(0xFFFFAD5C)

/** Панель на абсолютной позиции: ассет (inner + поля M с запечённой тенью) + контент
 *  в inner-зоне + янтарный двойной фокус-контур по кромке рамы (БЕЗ blur/scale). */
@Composable
private fun PanelBox(
    p: TvEskizSpec.P,
    s: Float,
    ax: (Float) -> Float,
    ay: (Float) -> Float,
    onClick: (() -> Unit)?,
    radArt: Float = 24f,
    center: Boolean = true,
    content: @Composable BoxScope.() -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val m = TvEskizSpec.M
    Box(
        modifier = Modifier
            .offset(x = ax(p.x - m).dp, y = ay(p.y - m).dp)
            .size(width = ((p.w + 2 * m) * s).dp, height = ((p.h + 2 * m) * s).dp)
            .drawWithContent {
                drawContent()
                if (!focused) return@drawWithContent
                val i1 = (m - 7f) * s * density
                val r1 = (radArt + 8f) * s * density
                drawRoundRect(
                    color = FocusGoldTv,
                    topLeft = Offset(i1, i1),
                    size = Size(size.width - 2 * i1, size.height - 2 * i1),
                    cornerRadius = CornerRadius(r1, r1),
                    style = Stroke(width = 3f * s * density),
                )
                val i2 = (m - 13f) * s * density
                val r2 = (radArt + 13f) * s * density
                drawRoundRect(
                    color = FocusAmberTv.copy(alpha = 0.65f),
                    topLeft = Offset(i2, i2),
                    size = Size(size.width - 2 * i2, size.height - 2 * i2),
                    cornerRadius = CornerRadius(r2, r2),
                    style = Stroke(width = 2.2f * s * density),
                )
            },
    ) {
        ArtImage(p.res, Modifier.fillMaxSize(), ContentScale.FillBounds)
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding((m * s).dp)
                .then(
                    if (onClick != null) {
                        Modifier.clickable(interactionSource = interaction, indication = null) { onClick() }
                    } else Modifier,
                ),
            contentAlignment = if (center) Alignment.Center else Alignment.CenterStart,
            content = content,
        )
    }
}

/** Заголовок секции слева (Playfair-золото, как на телефоне — без орнамент-línий). */
@Composable
private fun SectionHeader(title: String, yCenter: Float, s: Float, ax: (Float) -> Float, ay: (Float) -> Float) {
    Box(
        modifier = Modifier
            .offset(x = ax(TvEskizSpec.X0 + 6f).dp, y = ay(yCenter - 24f).dp)
            .size(width = ((TvEskizSpec.X1 - TvEskizSpec.X0) * s).dp, height = (48f * s).dp),
        contentAlignment = Alignment.CenterStart,
    ) {
        Text(
            title,
            color = GoldHi,
            fontFamily = PlayfairFamily,
            fontWeight = FontWeight.Bold,
            fontSize = (34f * s).sp,
            letterSpacing = 4.sp,
            maxLines = 1,
        )
    }
}

/** Живая премиальная плашка аккаунта: крупные читаемые данные без растра с текстом. */
@Composable
private fun AccountBarContent(login: String?, daysLeft: Int?, expires: String?, s: Float) {
    val expired = daysLeft != null && daysLeft <= 0
    val low = daysLeft != null && daysLeft in 1..5
    val daysColor = if (expired) Color(0xFFE5484D) else if (low) MaestroOrange else NeonGreen
    val daysText = when {
        daysLeft == null -> null
        expired -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит"
        else -> "Осталось $daysLeft ${daysWord(daysLeft)}" + (expires?.let { " · до $it" } ?: "")
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxSize().padding(horizontal = (24f * s).dp)) {
        GreenSquare(Icons.Filled.Person, s, 58f)
        Spacer(Modifier.width((22f * s).dp))
        Column(modifier = Modifier.weight(1f)) {
            if (!login.isNullOrBlank()) {
                Text("Аккаунт", color = Color(0xFFB7B9B5), fontWeight = FontWeight.Bold, fontSize = (18f * s).sp, maxLines = 1)
                Text(login, color = IvoryText, fontWeight = FontWeight.Bold, fontSize = (31f * s).sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (daysText != null) {
                Spacer(Modifier.height((4f * s).dp))
                Text(daysText, color = daysColor, fontWeight = FontWeight.Bold, fontSize = (23f * s).sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        Spacer(Modifier.width((18f * s).dp))
        GreenSquare(Icons.Filled.CalendarMonth, s, 52f)
    }
}

@Composable
private fun GreenSquare(icon: ImageVector, s: Float, artSize: Float) {
    Box(
        modifier = Modifier
            .size((artSize * s).dp)
            .clip(RoundedCornerShape((16f * s).dp))
            .background(Brush.verticalGradient(listOf(Color(0xFF58C97A), Color(0xFF1E5A33)))),
        contentAlignment = Alignment.Center,
    ) {
        Icon(icon, null, tint = Color(0xFFF0FAF2), modifier = Modifier.size((artSize * 0.58f * s).dp))
    }
}

private fun protocolIconTv(tag: String): ImageVector = when (tag) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Bolt
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Hub
    "anytls" -> Icons.Filled.Lock
    "olcrtc" -> Icons.Filled.Videocam
    else -> Icons.Filled.Layers
}

/** Битмап с High-фильтрацией (Low мылит дерево/золото при масштабе на 4K). */
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

/** Медальон-кнопка: круглая тап-зона на сфере/глазу + янтарное фокус-кольцо по ободу. */
@Composable
private fun MedallionButton(
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    s: Float,
    modifier: Modifier,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    Box(
        modifier = modifier
            .focusRequester(focusRequester)
            .clickable(interactionSource = interaction, indication = null) { onToggle() }
            .drawBehind {
                if (focused) {
                    drawCircle(
                        color = FocusGoldTv,
                        radius = TvEskizSpec.RING_RVIS * s.dp.toPx(),
                        center = Offset(size.width / 2f, size.height / 2f),
                        style = Stroke(width = 3.dp.toPx()),
                    )
                }
            },
    )
}

package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Code
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.filled.Wifi
import androidx.compose.material.icons.filled.Layers
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val TvBackground = Color(0xFF07100D)
private val TvSurface = Color(0xFF101B18)
private val TvSurfaceRaised = Color(0xFF172622)
private val TvSurfaceSelected = Color(0xFF203B2C)
private val TvText = Color(0xFFF4F4EF)
private val TvMuted = Color(0xFFA8B7AF)
private val TvLine = Color(0xFF2B4940)
private val TvFocus = Color(0xFFFFC857)
private val TvError = Color(0xFFFF6B6B)

/** Responsive Android TV home. The phone branch stays in TvHomeScreen.kt untouched. */
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
    val context = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    val open: (String) -> Unit = { url ->
        runCatching {
            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }
    }
    val checkConnection: () -> Unit = {
        scope.launch(Dispatchers.IO) {
            val ok = runCatching {
                (java.net.URL("https://www.google.com/generate_204").openConnection() as java.net.HttpURLConnection).run {
                    connectTimeout = 5000
                    readTimeout = 5000
                    try { responseCode in 200..399 } finally { disconnect() }
                }
            }.getOrDefault(false)
            withContext(Dispatchers.Main) {
                Toast.makeText(context, if (ok) "Соединение работает" else "Нет соединения", Toast.LENGTH_SHORT).show()
            }
        }
    }
    val updateApp: () -> Unit = {
        (context as? Activity)?.let { activity ->
            scope.launch(Dispatchers.IO) { runCatching { Vendor.checkUpdate(activity, true) } }
        }
    }

    androidx.compose.foundation.layout.BoxWithConstraints(
        modifier = Modifier
            .fillMaxSize()
            .background(TvBackground)
            .drawBehind {
                drawCircle(
                    brush = Brush.radialGradient(
                        listOf(
                            NeonGreen.copy(alpha = if (connected) 0.16f else 0.07f),
                            NeonGreen.copy(alpha = 0.02f),
                            Color.Transparent,
                        ),
                        center = Offset(size.width * 0.19f, size.height * 0.46f),
                        radius = size.minDimension * 0.58f,
                    ),
                    radius = size.minDimension * 0.58f,
                    center = Offset(size.width * 0.19f, size.height * 0.46f),
                )
                drawLine(
                    color = TvLine.copy(alpha = 0.5f),
                    start = Offset(size.width * 0.37f, 0f),
                    end = Offset(size.width * 0.37f, size.height),
                    strokeWidth = 1f,
                )
            }
            .padding(horizontal = 34.dp, vertical = 26.dp),
    ) {
        val compact = maxHeight < 560.dp
        val gap = if (compact) 7.dp else 12.dp
        val actionHeight = if (compact) 64.dp else 72.dp
        val smallButtonHeight = if (compact) 42.dp else 52.dp
        val protocolButtonHeight = 58.dp
        val protocolRowGap = if (compact) 6.dp else 10.dp
        val phoneWeight = if (compact) 1.65f else 1.55f

        Column(modifier = Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(gap)) {
            TvHeader(
                connected = connected,
                statusText = statusText,
            )

            Row(
                modifier = Modifier.weight(1f).fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(24.dp),
            ) {
                TvHeroPane(
                    connected = connected,
                    statusText = statusText,
                    activeProtocol = activeProtocol ?: selected,
                    accountLogin = accountLogin,
                    daysLeft = daysLeft,
                    accountExpires = accountExpires,
                    hasSubProfile = hasSubProfile,
                    onToggleConnect = onToggleConnect,
                    onEnterTrial = onEnterTrial,
                    connectFocus = connectFocus,
                    compact = compact,
                    modifier = Modifier.fillMaxHeight().weight(0.37f),
                )

                Column(
                    modifier = Modifier.fillMaxHeight().weight(0.63f),
                    verticalArrangement = Arrangement.spacedBy(gap),
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        TvActionButton(
                            label = "Купить подписку",
                            supporting = "Продлить доступ",
                            icon = Icons.Filled.ShoppingCart,
                            accent = true,
                            onClick = onBuy,
                            modifier = Modifier.weight(1f),
                            height = actionHeight,
                        )
                    }

                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        TvActionButton("Ввести код", "Активация", Icons.Filled.Code, onClick = onEnterCode, modifier = Modifier.weight(1f), height = actionHeight)
                        TvActionButton("VPN приложения", "Раздельный туннель", Icons.Filled.Public, onClick = onSplitTunnel, modifier = Modifier.weight(1f), height = actionHeight)
                        TvActionButton("Поделиться", "Подключить iPhone", Icons.Filled.Share, onClick = onShareIos, modifier = Modifier.weight(1f), height = actionHeight)
                    }

                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        TvSmallButton("Обновить приложение", Icons.Filled.CloudDownload, updateApp, Modifier.weight(1f), height = smallButtonHeight)
                        TvSmallButton("Проверить соединение", Icons.Filled.Wifi, checkConnection, Modifier.weight(1f), height = smallButtonHeight)
                    }

                    TvSectionTitle("ПОДДЕРЖКА")
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        TvSmallButton("8 977 811-65-64", Icons.Filled.Call, { open("tel:+79778116564") }, Modifier.weight(phoneWeight), height = smallButtonHeight)
                        TvSmallButton("Telegram", Icons.Filled.Send, { open("https://t.me/wapmixx") }, Modifier.weight(1f), iconTint = Color(0xFF2AABEE), height = smallButtonHeight)
                        TvSmallButton("WhatsApp", Icons.Filled.Chat, { open("https://wa.me/79778116564") }, Modifier.weight(1f), iconTint = Color(0xFF25D366), height = smallButtonHeight)
                        TvSmallButton("МАКС", Icons.Filled.Forum, { open("https://max.ru/") }, Modifier.weight(0.75f), iconTint = Color(0xFF4C9EFF), height = smallButtonHeight)
                    }

                    TvSectionTitle("ПРОТОКОЛЫ")
                    TvProtocolGrid(
                        protocols = protocols,
                        selected = selected,
                        hasOlcrtcCreds = hasOlcrtcCreds,
                        olcrtcProvider = olcrtcProvider,
                        onSelectProtocol = onSelectProtocol,
                        onSelectOlcrtc = onSelectOlcrtc,
                        modifier = Modifier.fillMaxWidth(),
                        buttonHeight = protocolButtonHeight,
                        rowGap = protocolRowGap,
                        showBadges = !compact,
                    )
                }
            }
        }
    }
}

@Composable
private fun TvHeader(connected: Boolean, statusText: String) {
    Row(
        modifier = Modifier.fillMaxWidth().height(54.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "MaestroVPN",
            color = TvText,
            fontFamily = PlayfairFamily,
            fontSize = 30.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.width(18.dp))
        Box(
            modifier = Modifier
                .clip(RoundedCornerShape(50))
                .background(if (connected) NeonGreen.copy(alpha = 0.14f) else TvSurface)
                .padding(horizontal = 12.dp, vertical = 7.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(8.dp).clip(CircleShape).background(if (connected) NeonGreen else TvError))
                Spacer(Modifier.width(8.dp))
                Text(statusText, color = if (connected) NeonGreen else TvMuted, fontSize = 15.sp, fontWeight = FontWeight.Bold)
            }
        }
        Spacer(Modifier.weight(1f))
        Text("Защищённое подключение", color = TvMuted, fontSize = 15.sp)
    }
}

@Composable
private fun TvHeroPane(
    connected: Boolean,
    statusText: String,
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    hasSubProfile: Boolean,
    onToggleConnect: () -> Unit,
    onEnterTrial: () -> Unit,
    connectFocus: FocusRequester,
    compact: Boolean,
    modifier: Modifier,
) {
    Column(
        modifier = modifier,
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.SpaceBetween,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text("СТАТУС", color = TvMuted, fontSize = 13.sp, fontWeight = FontWeight.Bold, letterSpacing = 2.sp)
            Spacer(Modifier.height(6.dp))
            Text(
                if (connected) "ПОДКЛЮЧЕНО" else "ОТКЛЮЧЕНО",
                color = if (connected) NeonGreen else TvError,
                fontSize = if (compact) 27.sp else 32.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 1.sp,
            )
            if (!activeProtocol.isNullOrBlank()) {
                Spacer(Modifier.height(4.dp))
                Text(
                    if (connected) "Активный протокол: ${tvProtocolLabel(activeProtocol)}" else "Готово: ${tvProtocolLabel(activeProtocol)}",
                    color = MaestroOrange,
                    fontSize = 14.sp,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }

        TvConnectButton(connected, onToggleConnect, connectFocus, compact)

        if (hasSubProfile || !accountLogin.isNullOrBlank() || daysLeft != null) {
            TvAccountCard(accountLogin, daysLeft, accountExpires)
        } else {
            TvActionButton(
                label = "Попробовать бесплатно",
                supporting = "2 дня без оплаты",
                icon = Icons.Filled.Info,
                onClick = onEnterTrial,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun TvConnectButton(connected: Boolean, onClick: () -> Unit, focusRequester: FocusRequester, compact: Boolean) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val diameter = if (compact) 194.dp else 226.dp
    Box(
        modifier = Modifier
            .size(diameter + 18.dp)
            .focusRequester(focusRequester)
            .clip(CircleShape)
            .background(if (connected) NeonGreen.copy(alpha = 0.13f) else TvSurface)
            .border(if (focused) 4.dp else 1.dp, if (focused) TvFocus else TvLine, CircleShape)
            .padding(9.dp)
            .clip(CircleShape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Icon(Icons.Filled.Shield, null, tint = if (connected) NeonGreen else TvMuted, modifier = Modifier.size(42.dp))
            Spacer(Modifier.height(4.dp))
            Text("${if (connected) "ОТКЛЮЧИТЬ" else "ПОДКЛЮЧИТЬ"}", color = TvText, fontSize = 15.sp, fontWeight = FontWeight.Bold)
        }
    }
}

@Composable
private fun TvAccountCard(login: String?, daysLeft: Int?, expires: String?) {
    val daysText = when {
        daysLeft == null -> "Статус подписки неизвестен"
        daysLeft <= 0 -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит"
        else -> "Осталось $daysLeft ${tvDaysWord(daysLeft)}" + (expires?.let { " · до $it" } ?: "")
    }
    val daysColor = when {
        daysLeft != null && daysLeft <= 0 -> TvError
        daysLeft != null && daysLeft <= 5 -> MaestroOrange
        else -> NeonGreen
    }
    TvSurfaceCard(modifier = Modifier.fillMaxWidth(), contentPadding = 16.dp) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                modifier = Modifier.size(46.dp).clip(RoundedCornerShape(12.dp)).background(NeonGreen.copy(alpha = 0.16f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(Icons.Filled.Info, null, tint = NeonGreen, modifier = Modifier.size(25.dp))
            }
            Spacer(Modifier.width(14.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text("АККАУНТ", color = TvMuted, fontSize = 11.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.5.sp)
                Text(login ?: "Без профиля", color = TvText, fontSize = 21.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(daysText, color = daysColor, fontSize = 14.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Icon(Icons.Filled.CalendarMonth, null, tint = TvMuted, modifier = Modifier.size(22.dp))
        }
    }
}

@Composable
private fun TvProtocolGrid(
    protocols: List<String>,
    selected: String?,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit,
    modifier: Modifier,
    buttonHeight: Dp = 58.dp,
    rowGap: Dp = 10.dp,
    showBadges: Boolean = true,
) {
    val display = (if (protocols.contains("olcrtc")) protocols else protocols + "olcrtc").take(8)
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(rowGap)) {
        display.chunked(4).forEach { row ->
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                row.forEach { protocol ->
                    val locked = protocol == "olcrtc" && !hasOlcrtcCreds
                    val isSelected = protocol == selected && !locked
                    TvProtocolButton(
                        protocol = protocol,
                        selected = isSelected,
                        locked = locked,
                        provider = olcrtcProvider,
                        onClick = { if (locked) onSelectOlcrtc() else onSelectProtocol(protocol) },
                        modifier = Modifier.weight(1f),
                        height = buttonHeight,
                        showBadge = showBadges,
                    )
                }
                for (index in row.size until 4) {
                    Spacer(Modifier.weight(1f))
                }
            }
        }
    }
}

@Composable
private fun TvProtocolButton(
    protocol: String,
    selected: Boolean,
    locked: Boolean,
    provider: String?,
    onClick: () -> Unit,
    modifier: Modifier,
    height: Dp,
    showBadge: Boolean,
) {
    TvSurfaceCard(
        modifier = modifier.height(height),
        selected = selected,
        onClick = onClick,
        contentPadding = if (showBadge) 10.dp else 8.dp,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(
                if (locked) Icons.Filled.Lock else tvProtocolIcon(protocol),
                null,
                tint = if (locked) TvMuted else if (selected) MaestroOrange else NeonGreen,
                modifier = Modifier.size(23.dp),
            )
            Spacer(Modifier.width(10.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(tvProtocolLabel(protocol), color = if (selected) MaestroOrange else TvText, fontSize = if (showBadge) 15.sp else 16.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (showBadge) {
                    Text(
                        when {
                            locked -> "по запросу"
                            protocol == "olcrtc" -> if (provider == "wbstream") "через WB" else "через Яндекс"
                            else -> tvProtocolBadge(protocol)
                        },
                        color = if (locked) TvMuted else NeonGreen,
                        fontSize = 10.sp,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

@Composable
private fun TvActionButton(
    label: String,
    supporting: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    accent: Boolean = false,
    height: Dp = 72.dp,
) {
    val compact = height < 70.dp
    TvSurfaceCard(
        modifier = modifier.height(height),
        onClick = onClick,
        selected = accent,
        contentPadding = if (compact) 8.dp else 14.dp,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, tint = if (accent) NeonGreen else NeonGreen, modifier = Modifier.size(if (compact) 22.dp else 27.dp))
            Spacer(Modifier.width(if (compact) 8.dp else 12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(label, color = if (accent) TvText else TvText, fontSize = if (compact) 15.sp else 17.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(supporting, color = TvMuted, fontSize = if (compact) 10.sp else 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable
private fun TvSmallButton(
    label: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    iconTint: Color = NeonGreen,
    height: Dp = 52.dp,
) {
    val compact = height < 48.dp
    TvSurfaceCard(modifier = modifier.height(height), onClick = onClick, contentPadding = if (compact) 8.dp else 10.dp) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
            Icon(icon, null, tint = iconTint, modifier = Modifier.size(if (compact) 18.dp else 20.dp))
            Spacer(Modifier.width(if (compact) 7.dp else 9.dp))
            Text(label, color = TvText, fontSize = if (compact) 13.sp else 14.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun TvIconButton(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, onClick: () -> Unit) {
    TvSurfaceCard(modifier = Modifier.size(72.dp), onClick = onClick, contentPadding = 10.dp) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Icon(icon, label, tint = NeonGreen, modifier = Modifier.size(24.dp))
            Text(label, color = TvMuted, fontSize = 9.sp, maxLines = 1)
        }
    }
}

@Composable
private fun TvSectionTitle(title: String) {
    Text(title, color = TvMuted, fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 2.sp)
}

@Composable
private fun TvSurfaceCard(
    modifier: Modifier = Modifier,
    selected: Boolean = false,
    onClick: (() -> Unit)? = null,
    contentPadding: Dp = 16.dp,
    content: @Composable () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(16.dp))
            .background(if (selected) TvSurfaceSelected else TvSurfaceRaised)
            .border(if (focused) 3.dp else 1.dp, if (focused) TvFocus else TvLine, RoundedCornerShape(16.dp))
            .then(if (onClick != null) Modifier.clickable(interactionSource = interaction, indication = null, onClick = onClick) else Modifier)
            .padding(contentPadding),
        contentAlignment = Alignment.CenterStart,
    ) {
        content()
    }
}

private fun tvProtocolIcon(protocol: String) = when (protocol) {
    "auto" -> Icons.Filled.Speed
    "hysteria2" -> Icons.Filled.Layers
    "vless" -> Icons.Filled.Shield
    "naive" -> Icons.Filled.Public
    "anytls" -> Icons.Filled.Lock
    "olcrtc" -> Icons.Filled.Videocam
    else -> Icons.Filled.Wifi
}

private fun tvProtocolLabel(protocol: String) = when (protocol.lowercase()) {
    "auto" -> "Авто"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "Naive"
    "anytls" -> "AnyTLS"
    "olcrtc" -> "OLC RTC"
    else -> protocol
}

private fun tvProtocolBadge(protocol: String) = when (protocol.lowercase()) {
    "auto" -> "автовыбор"
    "hysteria2" -> "быстрый"
    "vless" -> "универсальный"
    "naive" -> "маскировка"
    "anytls" -> "стабильный"
    else -> "доступен"
}

private fun tvDaysWord(days: Int): String = when {
    days % 100 in 11..14 -> "дней"
    days % 10 == 1 -> "день"
    days % 10 in 2..4 -> "дня"
    else -> "дней"
}

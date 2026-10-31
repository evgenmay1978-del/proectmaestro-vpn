package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Image
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
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Code
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.filled.Wifi
import androidx.compose.material.icons.filled.VpnLock
import androidx.compose.material.icons.filled.Layers
import androidx.compose.material.icons.outlined.VpnKey
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.model.visibleActiveProtocol
import com.maestrovpn.tv.compose.model.visibleProtocolTags
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.utils.ConnectivityCheck
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val TvBackground = Color(0xFF070909)
private val TvSurface = Color(0xFF101313)
private val TvSurfaceRaised = Color(0xFF171B1A)
private val TvSurfaceSelected = Color(0xFF14261B)
private val TvText = Color(0xFFF3F0E8)
private val TvMuted = Color(0xFFA7AAA3)
private val TvLine = Color(0xFF353A37)
private val TvFocus = Color(0xFFE6BE76)
private val TvGold = Color(0xFFC9A15E)
private val TvGreen = Color(0xFF51B56E)
private val TvError = Color(0xFFE06B62)

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
    onOpenAivpn: () -> Unit = {},
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
            val ok = ConnectivityCheck.isOnline()
            withContext(Dispatchers.Main) {
                Toast.makeText(context, ConnectivityCheck.verdict(ok, connected), Toast.LENGTH_LONG).show()
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
                            TvGreen.copy(alpha = if (connected) 0.13f else 0.04f),
                            TvGold.copy(alpha = 0.025f),
                            Color.Transparent,
                        ),
                        center = Offset(size.width * 0.16f, size.height * 0.48f),
                        radius = size.minDimension * 0.64f,
                    ),
                    radius = size.minDimension * 0.64f,
                    center = Offset(size.width * 0.16f, size.height * 0.48f),
                )
                drawCircle(
                    brush = Brush.radialGradient(
                        listOf(TvGold.copy(alpha = 0.07f), Color.Transparent),
                        center = Offset(size.width * 0.79f, size.height * 0.42f),
                        radius = size.minDimension * 0.72f,
                    ),
                    radius = size.minDimension * 0.72f,
                    center = Offset(size.width * 0.79f, size.height * 0.42f),
                )
                drawLine(
                    color = TvLine.copy(alpha = 0.75f),
                    start = Offset(size.width * 0.305f, 0f),
                    end = Offset(size.width * 0.305f, size.height),
                    strokeWidth = 1f,
                )
                drawLine(
                    color = TvLine.copy(alpha = 0.22f),
                    start = Offset(size.width * 0.61f, size.height * 0.05f),
                    end = Offset(size.width * 0.96f, size.height * 0.86f),
                    strokeWidth = 3f,
                )
                drawLine(
                    color = TvGold.copy(alpha = 0.05f),
                    start = Offset(size.width * 0.72f, size.height * 0.02f),
                    end = Offset(size.width * 0.98f, size.height * 0.62f),
                    strokeWidth = 8f,
                )
            }
            .padding(horizontal = 30.dp, vertical = 24.dp),
    ) {
        val compact = maxHeight < 600.dp
        val gap = if (compact) 7.dp else 10.dp
        val actionHeight = if (compact) 64.dp else 72.dp
        val smallButtonHeight = if (compact) 44.dp else 50.dp

        Image(
            painter = painterResource(R.mipmap.ic_launcher_foreground),
            contentDescription = null,
            modifier = Modifier
                .align(Alignment.CenterEnd)
                .size(if (compact) 360.dp else 460.dp)
                .alpha(0.035f),
        )

        Row(
            modifier = Modifier.fillMaxSize(),
            horizontalArrangement = Arrangement.spacedBy(22.dp),
        ) {
            TvHeroPane(
                connected = connected,
                statusText = statusText,
                activeProtocol = visibleActiveProtocol(activeProtocol, selected),
                accountLogin = accountLogin,
                daysLeft = daysLeft,
                accountExpires = accountExpires,
                hasSubProfile = hasSubProfile,
                onToggleConnect = onToggleConnect,
                onEnterTrial = onEnterTrial,
                connectFocus = connectFocus,
                compact = compact,
                modifier = Modifier.fillMaxHeight().weight(0.29f),
            )

            Column(
                modifier = Modifier.fillMaxHeight().weight(0.71f),
                verticalArrangement = Arrangement.spacedBy(gap),
            ) {
                TvActionButton(
                    label = "Купить подписку",
                    supporting = "Продлить защищённый доступ",
                    icon = Icons.Filled.ShoppingCart,
                    accent = true,
                    onClick = onBuy,
                    modifier = Modifier.fillMaxWidth(),
                    height = actionHeight,
                )

                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    TvActionButton("Ввести код", "Активация", Icons.Filled.Code, onClick = onEnterCode, modifier = Modifier.weight(1f), height = actionHeight)
                    TvActionButton("Приложения", "VPN-туннель", Icons.Filled.Public, onClick = onSplitTunnel, modifier = Modifier.weight(1f), height = actionHeight)
                    TvActionButton("Поделиться", "Подключить iPhone", Icons.Filled.Share, onClick = onShareIos, modifier = Modifier.weight(1f), height = actionHeight)
                    // AIVPN is a separate VpnService + own UDP tunnel, not a sing-box outbound, so it
                    // gets its own tile instead of a chip inside TVProtocolGrid. Its label
                    // ("НейроПротокол"/"NeuroProtocol") is the longest in the row, so the tile takes a
                    // slightly wider slot and the name is not ellipsized on a 960dp panel.
                    TvActionButton(
                        stringResource(R.string.aivpn_settings),
                        stringResource(R.string.aivpn_settings_summary),
                        Icons.Outlined.VpnKey,
                        onClick = onOpenAivpn,
                        modifier = Modifier.weight(1.3f),
                        height = actionHeight,
                    )
                }

                // Telegram / WhatsApp / МАКС убраны с ТВ (owner 2026-10-25): на боксе без
                // браузера и звонилки они открывали только заглушку frameworkpackagestubs.
                // Телефон поддержки остался и переехал в этот ряд — так освободившееся место
                // уходит сетке протоколов, а не пустой полосе «ПОДДЕРЖКА».
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    TvSmallButton("Обновить приложение", Icons.Filled.CloudDownload, updateApp, Modifier.weight(1.25f), height = smallButtonHeight)
                    TvSmallButton("Проверить соединение", Icons.Filled.Wifi, checkConnection, Modifier.weight(1.25f), height = smallButtonHeight)
                    TvSmallButton("8 977 811-65-64", Icons.Filled.Call, { open("tel:+79778116564") }, Modifier.weight(1f), height = smallButtonHeight)
                }

                TvSectionTitle("ПРОТОКОЛЫ")
                // weight(1f): сетка забирает всю оставшуюся высоту и заканчивается на одной
                // линии с левой панелью — крупные плашки вместо мелких с пустотой внизу.
                TvProtocolGrid(
                    protocols = protocols,
                    selected = selected,
                    hasOlcrtcCreds = hasOlcrtcCreds,
                    olcrtcProvider = olcrtcProvider,
                    onSelectProtocol = onSelectProtocol,
                    onSelectOlcrtc = onSelectOlcrtc,
                    modifier = Modifier.fillMaxWidth().weight(1f),
                    compact = compact,
                )
            }
        }
    }
}

@Composable
private fun TvHeader(connected: Boolean, statusText: String, compact: Boolean) {
    Column(
        modifier = Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Image(
            painter = painterResource(R.mipmap.ic_launcher_foreground),
            contentDescription = "MaestroVPN",
            modifier = Modifier.size(if (compact) 68.dp else 82.dp),
        )
        Text(
            "MaestroVPN",
            color = TvText,
            fontFamily = PlayfairFamily,
            fontSize = if (compact) 23.sp else 27.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.height(if (compact) 5.dp else 8.dp))
        Box(
            modifier = Modifier
                .clip(RoundedCornerShape(50))
                .background(if (connected) TvGreen.copy(alpha = 0.13f) else TvSurface)
                .border(1.dp, if (connected) TvGreen.copy(alpha = 0.45f) else TvLine, RoundedCornerShape(50))
                .padding(horizontal = 12.dp, vertical = 6.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(7.dp).clip(CircleShape).background(if (connected) TvGreen else TvError))
                Spacer(Modifier.width(8.dp))
                Text(
                    statusText,
                    color = if (connected) TvGreen else TvMuted,
                    fontSize = if (compact) 12.sp else 14.sp,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
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
        modifier = modifier
            .clip(RoundedCornerShape(20.dp))
            .background(
                Brush.verticalGradient(
                    listOf(TvSurfaceRaised.copy(alpha = 0.94f), TvSurface.copy(alpha = 0.98f)),
                ),
            )
            .border(1.dp, TvLine.copy(alpha = 0.9f), RoundedCornerShape(20.dp))
            .padding(horizontal = if (compact) 14.dp else 18.dp, vertical = if (compact) 12.dp else 16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.SpaceBetween,
    ) {
        TvHeader(connected = connected, statusText = statusText, compact = compact)

        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                if (connected) "ПОДКЛЮЧЕНО" else "ОТКЛЮЧЕНО",
                color = if (connected) TvGreen else TvError,
                fontSize = if (compact) 20.sp else 24.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 1.sp,
            )
            if (!activeProtocol.isNullOrBlank()) {
                Text(
                    if (connected) "Активный протокол: ${tvProtocolLabel(activeProtocol)}" else "Готово: ${tvProtocolLabel(activeProtocol)}",
                    color = TvGold,
                    fontSize = if (compact) 11.sp else 13.sp,
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
    val diameter = if (compact) 128.dp else 164.dp
    Box(
        modifier = Modifier
            .size(diameter + 22.dp)
            .focusRequester(focusRequester)
            .clip(CircleShape)
            .background(
                Brush.radialGradient(
                    listOf(
                        if (connected) TvGreen.copy(alpha = 0.28f) else TvGold.copy(alpha = 0.13f),
                        TvSurface,
                    ),
                ),
            )
            .border(if (focused) 4.dp else 2.dp, if (focused) TvFocus else TvGold.copy(alpha = 0.42f), CircleShape)
            .padding(10.dp)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(diameter)
                .clip(CircleShape)
                .background(
                    Brush.verticalGradient(
                        listOf(Color(0xFF242927), Color(0xFF0D1010)),
                    ),
                )
                .border(1.dp, TvLine, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(
                    Icons.Filled.PowerSettingsNew,
                    null,
                    tint = if (connected) TvGreen else TvGold,
                    modifier = Modifier.size(if (compact) 36.dp else 44.dp),
                )
                Spacer(Modifier.height(5.dp))
                Text(
                    if (connected) "ОТКЛЮЧИТЬ" else "ПОДКЛЮЧИТЬ",
                    color = TvText,
                    fontSize = if (compact) 12.sp else 14.sp,
                    fontWeight = FontWeight.Bold,
                    letterSpacing = 0.7.sp,
                )
            }
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
        daysLeft != null && daysLeft <= 5 -> TvGold
        else -> TvGreen
    }
    TvSurfaceCard(modifier = Modifier.fillMaxWidth(), contentPadding = 12.dp) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                modifier = Modifier.size(40.dp).clip(RoundedCornerShape(11.dp)).background(TvGold.copy(alpha = 0.13f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(Icons.Filled.Info, null, tint = TvGold, modifier = Modifier.size(22.dp))
            }
            Spacer(Modifier.width(11.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text("АККАУНТ", color = TvMuted, fontSize = 11.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.5.sp)
                Text(login ?: "Без профиля", color = TvText, fontSize = 16.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(daysText, color = daysColor, fontSize = 11.sp, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Icon(Icons.Filled.CalendarMonth, null, tint = TvMuted, modifier = Modifier.size(19.dp))
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
    compact: Boolean,
) {
    val display = visibleTvProtocols(protocols)
    val columns = 4
    val rows = display.chunked(columns)
    androidx.compose.foundation.layout.BoxWithConstraints(modifier = modifier) {
        val gap = 10.dp
        val rowCount = rows.size.coerceAtLeast(1)
        val available = (maxHeight - gap * (rowCount - 1)) / rowCount
        // На панели владельца (1920×1080 px, density 320 → 960×540 dp) выходит 129 dp —
        // ровно как в согласованном макете. Верхняя граница нужна панелям, где dp совпадает
        // с пикселем (1920×1080 dp): без неё карточки растянулись бы на пол-экрана.
        val rowHeight = available.coerceIn(72.dp, if (compact) 132.dp else 150.dp)
        Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(gap)) {
            rows.forEach { row ->
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(gap)) {
                    row.forEach { protocol ->
                        val locked = protocol == "olcrtc" && !hasOlcrtcCreds
                        TvProtocolCard(
                            protocol = protocol,
                            selected = protocol == selected && !locked,
                            locked = locked,
                            provider = olcrtcProvider,
                            onClick = { if (locked) onSelectOlcrtc() else onSelectProtocol(protocol) },
                            modifier = Modifier.weight(1f).height(rowHeight),
                        )
                    }
                    for (index in row.size until columns) {
                        Spacer(Modifier.weight(1f))
                    }
                }
            }
        }
    }
}

internal fun visibleTvProtocols(protocols: List<String>): List<String> =
    if (protocols.isEmpty()) {
        listOf("auto", "vless", "hysteria2", "naive", "anytls", "vless-s3", "awg")
    } else {
        visibleProtocolTags(protocols).take(8)
    }

@Composable
private fun TvProtocolCard(
    protocol: String,
    selected: Boolean,
    locked: Boolean,
    provider: String?,
    onClick: () -> Unit,
    modifier: Modifier,
) {
    val model = tvProtocolCardModel(protocol, locked, provider)
    TvSurfaceCard(modifier = modifier, selected = selected, onClick = onClick, contentPadding = 12.dp) {
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            if (model.flag.isNotEmpty()) {
                Text(model.flag, fontSize = 30.sp, lineHeight = 34.sp, maxLines = 1)
            } else {
                Icon(
                    model.icon,
                    null,
                    tint = if (locked) TvMuted else TvGreen,
                    modifier = Modifier.size(32.dp),
                )
            }
            Spacer(Modifier.height(8.dp))
            Text(
                model.title,
                color = if (selected) TvGreen else TvText,
                fontSize = 18.sp,
                lineHeight = 24.sp,
                fontWeight = FontWeight.Bold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(2.dp))
            Text(
                model.subtitle,
                color = if (selected) TvGreen else TvMuted,
                fontSize = 12.sp,
                lineHeight = 16.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
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
        selected = false,
        contentPadding = if (compact) 8.dp else 14.dp,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, tint = if (accent) TvGold else TvGreen, modifier = Modifier.size(if (compact) 22.dp else 27.dp))
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
    iconTint: Color = TvGreen,
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
            Icon(icon, label, tint = TvGreen, modifier = Modifier.size(24.dp))
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
            .background(
                Brush.verticalGradient(
                    if (selected) {
                        listOf(TvSurfaceSelected, TvGreen.copy(alpha = 0.09f))
                    } else {
                        listOf(Color(0xFF1B1F1E), TvSurfaceRaised)
                    },
                ),
            )
            .border(
                if (focused) 3.dp else if (selected) 2.dp else 1.dp,
                if (focused) TvFocus else if (selected) TvGreen.copy(alpha = 0.72f) else TvLine,
                RoundedCornerShape(16.dp),
            )
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
    "awg" -> Icons.Filled.VpnLock
    else -> Icons.Filled.Wifi
}

private fun tvProtocolLabel(protocol: String) = when (protocol.lowercase()) {
    "auto" -> "Авто"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "Naive"
    "anytls" -> "AnyTLS"
    "olcrtc" -> "OLC RTC"
    "awg" -> "AmneziaWG"
    else -> protocol
}

private fun tvProtocolBadge(protocol: String) = when (protocol.lowercase()) {
    "auto" -> "автовыбор"
    "hysteria2" -> "быстрый"
    "vless" -> "универсальный"
    "naive" -> "маскировка"
    "anytls" -> "стабильный"
    "awg" -> "маскировка"
    else -> "доступен"
}

/** Big protocol tile: flag (or icon) on top, then the country/name and the transport. */
private data class TvProtocolCardModel(
    val flag: String,
    val title: String,
    val subtitle: String,
    val icon: androidx.compose.ui.graphics.vector.ImageVector,
)

private val tvServerFlags = listOf(
    "🇪🇸" to "Испания",
    "🇨🇿" to "Чехия",
    "🇳🇱" to "Нидерланды",
    "🇩🇪" to "Германия",
)

/**
 * Maps a protocol tag to the tile. Two shapes actually reach us:
 *  - transport keys ("auto", "vless", "vless-s3", …) when the profile lists them separately;
 *  - server display tags, e.g. "🇪🇸 MaestroVPN VLESS S3" — that is what the live subscription
 *    ships, and the old one-line layout truncated them to "Mae…".
 */
private fun tvProtocolCardModel(protocol: String, locked: Boolean, provider: String?): TvProtocolCardModel {
    if (locked) return TvProtocolCardModel("", tvProtocolLabel(protocol), "по запросу", Icons.Filled.Lock)
    when (protocol.trim().lowercase()) {
        "auto", "urltest", "select" -> return TvProtocolCardModel("", "Авто", "автовыбор", Icons.Filled.Speed)
        "awg" -> return TvProtocolCardModel("", "AmneziaWG", "маскировка", Icons.Filled.VpnLock)
        "vless" -> return TvProtocolCardModel("🇪🇸", "Испания", "VLESS", Icons.Filled.Shield)
        "hysteria2" -> return TvProtocolCardModel("🇨🇿", "Чехия", "Hysteria2", Icons.Filled.Layers)
        "naive", "naiveproxy" -> return TvProtocolCardModel("🇨🇿", "Чехия", "Naive", Icons.Filled.Public)
        "anytls" -> return TvProtocolCardModel("🇨🇿", "Чехия", "AnyTLS", Icons.Filled.Lock)
        "vless-s3" -> return TvProtocolCardModel("🇳🇱", "Нидерланды", "VLESS S3", Icons.Filled.Shield)
        "vless-s4" -> return TvProtocolCardModel("🇩🇪", "Германия", "VLESS S4", Icons.Filled.Shield)
        "olcrtc", "olc rtc" -> return TvProtocolCardModel("", "OLC RTC", if (provider == "wbstream") "через WB" else "через Яндекс", Icons.Filled.Videocam)
    }
    val server = tvServerFlags.firstOrNull { protocol.contains(it.first) }
    if (server != null) {
        val transport = tvServerFlags
            .fold(protocol) { acc, (flag, _) -> acc.replace(flag, " ") }
            .replace("MaestroVPN", " ", ignoreCase = true)
            .split(' ')
            .filter { it.isNotBlank() }
            .joinToString(" ")
        return TvProtocolCardModel(server.first, server.second, transport.ifBlank { "сервер" }, Icons.Filled.Shield)
    }
    return TvProtocolCardModel("", tvProtocolLabel(protocol), tvProtocolBadge(protocol), tvProtocolIcon(protocol))
}

private fun tvDaysWord(days: Int): String = when {
    days % 100 in 11..14 -> "дней"
    days % 10 == 1 -> "день"
    days % 10 in 2..4 -> "дня"
    else -> "дней"
}


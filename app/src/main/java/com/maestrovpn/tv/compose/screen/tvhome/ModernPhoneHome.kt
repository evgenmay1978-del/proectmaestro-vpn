package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.ChevronRight
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.HeadsetMic
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Shield
import androidx.compose.material.icons.filled.ShoppingBag
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selectableGroup
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.premium.PremiumEmerald
import com.maestrovpn.tv.compose.premium.PremiumGold
import com.maestrovpn.tv.compose.premium.PremiumGoldMuted
import com.maestrovpn.tv.compose.premium.PremiumText
import com.maestrovpn.tv.compose.premium.PremiumTextMuted
import com.maestrovpn.tv.compose.premium.PremiumWalnut
import com.maestrovpn.tv.compose.theme.PlayfairFamily

private val ModernInk = Color(0xFF080B0E)
private val ModernPanel = Color(0xFF11171B)
private val ModernPanelRaised = Color(0xFF182126)
private val ModernLine = Color(0xFF2A393D)
private val ModernDanger = Color(0xFFE16B68)

/**
 * Phone-only home presentation. It deliberately avoids the old full-screen carved atlas: the
 * connection state and next action are now the first visual read, while the same callbacks and
 * protocol/account data remain intact.
 */
@Composable
internal fun ModernPhoneHome(
    statusText: String,
    connected: Boolean,
    connecting: Boolean,
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
    onScanQr: () -> Unit,
    onEnterTrial: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val scrollState = rememberScrollState()
    val stateColor = when {
        connected -> PremiumEmerald
        connecting -> PremiumGold
        else -> ModernDanger
    }
    val stateLabel = when {
        connecting -> "ПОДКЛЮЧЕНИЕ"
        connected -> "ЗАЩИЩЕНО"
        else -> "ОТКЛЮЧЕНО"
    }
    val protocol = activeProtocol ?: selected
    val protocolLabel = protocol?.let(::protocolLabel) ?: "Автовыбор"
    val subscriptionLabel = when {
        daysLeft == null -> "Подписка не активирована"
        daysLeft <= 0 -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимитный доступ"
        else -> "Осталось $daysLeft ${daysWord(daysLeft)}"
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(
                Brush.verticalGradient(
                    listOf(Color(0xFF10191C), ModernInk, Color(0xFF050708)),
                ),
            )
            .testTag("premium-phone-home"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .verticalScroll(scrollState)
                .padding(horizontal = 20.dp),
        ) {
            Spacer(Modifier.height(14.dp))
            ModernTopBar(onSettings = onSplitTunnel)
            Spacer(Modifier.height(20.dp))
            ModernAccountCard(
                login = accountLogin,
                subscriptionLabel = subscriptionLabel,
                expires = accountExpires,
                onClick = onEnterCode,
                modifier = Modifier.testTag("premium-account"),
            )
            Spacer(Modifier.height(24.dp))
            Text(
                text = "MaestroVPN",
                color = PremiumText,
                fontFamily = PlayfairFamily,
                fontSize = 30.sp,
                fontWeight = FontWeight.SemiBold,
            )
            Text(
                text = "Ваш интернет. Под защитой.",
                color = PremiumTextMuted,
                fontSize = 14.sp,
                modifier = Modifier.padding(top = 3.dp),
            )
            Spacer(Modifier.height(18.dp))
            ModernConnectButton(
                connected = connected,
                connecting = connecting,
                stateColor = stateColor,
                onClick = onToggleConnect,
            )
            Spacer(Modifier.height(14.dp))
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("premium-status"),
            ) {
                Text(stateLabel, color = stateColor, fontSize = 13.sp, fontWeight = FontWeight.Bold)
                Text(
                    text = if (connected) "Соединение активно" else statusText,
                    color = PremiumTextMuted,
                    fontSize = 13.sp,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            Spacer(Modifier.height(22.dp))
            ModernProtocolCard(
                protocols = protocols,
                selected = selected,
                activeProtocol = activeProtocol,
                hasOlcrtcCreds = hasOlcrtcCreds,
                olcrtcProvider = olcrtcProvider,
                onSelectProtocol = onSelectProtocol,
                onSelectOlcrtc = onSelectOlcrtc,
            )
            Spacer(Modifier.height(14.dp))
            ModernActionRow(
                icon = Icons.Default.ShoppingBag,
                title = "Продлить подписку",
                subtitle = "Доступ без ограничений",
                onClick = onBuy,
                modifier = Modifier.testTag("home-action-buy"),
            )
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
                ModernSmallAction(Icons.Default.QrCode2, "QR-код", onScanQr, Modifier.weight(1f))
                ModernSmallAction(Icons.Default.CloudDownload, "Пробный доступ", onEnterTrial, Modifier.weight(1f))
            }
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
                ModernSmallAction(Icons.Default.HeadsetMic, "Поддержка", onShareIos, Modifier.weight(1f))
                ModernSmallAction(Icons.Default.Settings, "Раздельный туннель", onSplitTunnel, Modifier.weight(1f))
            }
            Spacer(Modifier.height(28.dp))
            ModernBottomNav(
                onHome = {},
                onServers = { onSelectProtocol(selected ?: protocols.firstOrNull() ?: "auto") },
                onSubscription = onBuy,
                onSettings = onSplitTunnel,
            )
            Spacer(Modifier.height(12.dp))
        }
    }
}

@Composable
private fun ModernTopBar(onSettings: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier.size(42.dp).clip(CircleShape).background(PremiumEmerald.copy(alpha = 0.16f)),
        ) {
            Icon(Icons.Default.Shield, null, tint = PremiumEmerald, modifier = Modifier.size(22.dp))
        }
        Column(modifier = Modifier.weight(1f).padding(start = 12.dp)) {
            Text("MAESTRO", color = PremiumText, fontSize = 13.sp, fontWeight = FontWeight.Bold, letterSpacing = 2.sp)
            Text("VPN CONTROL", color = PremiumGoldMuted, fontSize = 10.sp, letterSpacing = 1.4.sp)
        }
        Icon(
            Icons.Default.Settings,
            contentDescription = "Настройки",
            tint = PremiumTextMuted,
            modifier = Modifier.size(24.dp).clickable(onClick = onSettings),
        )
    }
}

@Composable
private fun ModernAccountCard(login: String?, subscriptionLabel: String, expires: String?, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(18.dp))
            .background(ModernPanel)
            .border(1.dp, ModernLine, RoundedCornerShape(18.dp))
            .clickable(onClick = onClick)
            .padding(16.dp),
    ) {
        Box(contentAlignment = Alignment.Center, modifier = Modifier.size(42.dp).clip(CircleShape).background(PremiumGold.copy(alpha = 0.14f))) {
            Icon(Icons.Default.Lock, null, tint = PremiumGold, modifier = Modifier.size(20.dp))
        }
        Column(modifier = Modifier.weight(1f).padding(start = 12.dp)) {
            Text(login?.takeIf { it.isNotBlank() } ?: "Гостевой профиль", color = PremiumText, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
            Text(subscriptionLabel + (expires?.let { " · до $it" } ?: ""), color = PremiumTextMuted, fontSize = 12.sp, modifier = Modifier.padding(top = 3.dp), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Icon(Icons.Default.ChevronRight, null, tint = PremiumGoldMuted)
    }
}

@Composable
private fun ModernConnectButton(connected: Boolean, connecting: Boolean, stateColor: Color, onClick: () -> Unit) {
    Box(contentAlignment = Alignment.Center, modifier = Modifier.fillMaxWidth()) {
        Box(
            modifier = Modifier
                .size(176.dp)
                .shadow(22.dp, CircleShape, ambientColor = stateColor.copy(alpha = 0.22f), spotColor = stateColor.copy(alpha = 0.3f))
                .clip(CircleShape)
                .background(Brush.radialGradient(listOf(Color(0xFF283A3C), Color(0xFF10191B))))
                .border(1.dp, stateColor.copy(alpha = 0.65f), CircleShape)
                .clickable(onClick = onClick),
            contentAlignment = Alignment.Center,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(if (connected) Icons.Default.Shield else Icons.Default.Bolt, null, tint = stateColor, modifier = Modifier.size(38.dp))
                Text(if (connecting) "ЖДИТЕ" else if (connected) "СТОП" else "ПОДКЛЮЧИТЬ", color = PremiumText, fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.1.sp, modifier = Modifier.padding(top = 8.dp))
            }
        }
    }
}

@Composable
private fun ModernProtocolCard(protocols: List<String>, selected: String?, activeProtocol: String?, hasOlcrtcCreds: Boolean, olcrtcProvider: String?, onSelectProtocol: (String) -> Unit, onSelectOlcrtc: () -> Unit) {
    val options = orderedHomeProtocols(protocols, includeOwnerProtocols = hasOlcrtcCreds)
    Column(modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(ModernPanel).border(1.dp, ModernLine, RoundedCornerShape(18.dp)).padding(16.dp).semantics { selectableGroup() }) {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Column(modifier = Modifier.weight(1f)) {
                Text("МАРШРУТ", color = PremiumGoldMuted, fontSize = 11.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.3.sp)
                Text("Оптимальный сервер", color = PremiumText, fontSize = 16.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 3.dp))
            }
            Icon(Icons.Default.Speed, null, tint = PremiumEmerald, modifier = Modifier.size(22.dp))
        }
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
            options.take(3).forEach { tag ->
                val isSelected = (activeProtocol ?: selected) == tag
                ProtocolChip(tag, isSelected, { onSelectProtocol(tag) }, Modifier.weight(1f))
            }
        }
        if (hasOlcrtcCreds) {
            Text("Дополнительно: ${olcrtcProvider ?: "olcRTC"}", color = PremiumTextMuted, fontSize = 12.sp, modifier = Modifier.padding(top = 10.dp).clickable(onClick = onSelectOlcrtc))
        }
    }
}

@Composable
private fun ProtocolChip(tag: String, selected: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = modifier.clip(RoundedCornerShape(12.dp)).background(if (selected) PremiumEmerald.copy(alpha = 0.18f) else ModernPanelRaised).border(1.dp, if (selected) PremiumEmerald.copy(alpha = 0.7f) else ModernLine, RoundedCornerShape(12.dp)).selectable(selected, Role.RadioButton, onClick).padding(vertical = 11.dp, horizontal = 4.dp)) {
        Icon(if (tag == "auto") Icons.Default.Speed else Icons.Default.Shield, null, tint = if (selected) PremiumEmerald else PremiumGoldMuted, modifier = Modifier.size(18.dp))
        Text(protocolLabel(tag), color = if (selected) PremiumText else PremiumTextMuted, fontSize = 11.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 5.dp))
    }
}

@Composable
private fun ModernActionRow(icon: ImageVector, title: String, subtitle: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = modifier.fillMaxWidth().clip(RoundedCornerShape(16.dp)).background(ModernPanelRaised).border(1.dp, ModernLine, RoundedCornerShape(16.dp)).clickable(onClick = onClick).padding(15.dp)) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(25.dp))
        Column(modifier = Modifier.weight(1f).padding(start = 12.dp)) {
            Text(title, color = PremiumText, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
            Text(subtitle, color = PremiumTextMuted, fontSize = 12.sp, modifier = Modifier.padding(top = 3.dp))
        }
        Icon(Icons.Default.ChevronRight, null, tint = PremiumGoldMuted)
    }
}

@Composable
private fun ModernSmallAction(icon: ImageVector, label: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = modifier.clip(RoundedCornerShape(16.dp)).background(ModernPanel).border(1.dp, ModernLine, RoundedCornerShape(16.dp)).clickable(onClick = onClick).padding(vertical = 14.dp, horizontal = 6.dp)) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(22.dp))
        Text(label, color = PremiumTextMuted, fontSize = 11.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 8.dp))
    }
}

@Composable
private fun ModernBottomNav(onHome: () -> Unit, onServers: () -> Unit, onSubscription: () -> Unit, onSettings: () -> Unit) {
    Row(horizontalArrangement = Arrangement.SpaceAround, modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(Color(0xFF0D1316)).border(1.dp, ModernLine, RoundedCornerShape(18.dp)).padding(vertical = 10.dp)) {
        ModernNavItem(Icons.Default.Shield, "Главная", true, onHome)
        ModernNavItem(Icons.Default.Speed, "Серверы", false, onServers)
        ModernNavItem(Icons.Default.ShoppingBag, "Подписка", false, onSubscription)
        ModernNavItem(Icons.Default.Settings, "Настройки", false, onSettings)
    }
}

@Composable
private fun ModernNavItem(icon: ImageVector, label: String, selected: Boolean, onClick: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.clickable(onClick = onClick).padding(horizontal = 10.dp)) {
        Icon(icon, null, tint = if (selected) PremiumEmerald else PremiumTextMuted, modifier = Modifier.size(20.dp))
        Text(label, color = if (selected) PremiumText else PremiumTextMuted, fontSize = 10.sp, modifier = Modifier.padding(top = 5.dp))
    }
}

private fun daysWord(days: Int): String = when {
    days % 100 in 11..14 -> "дней"
    days % 10 == 1 -> "день"
    days % 10 in 2..4 -> "дня"
    else -> "дней"
}

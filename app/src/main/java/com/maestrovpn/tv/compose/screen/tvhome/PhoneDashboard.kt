package com.maestrovpn.tv.compose.screen.tvhome

import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.premium.*
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.whitelist.WhiteListSelection

/** Owner-approved phone layout; all actions retain the existing account and VPN flows. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun PhoneDashboard(
    connected: Boolean,
    connecting: Boolean,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    hasSubProfile: Boolean,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onOpenServers: () -> Unit,
    onOpenSettings: () -> Unit,
    onSplitTunnel: () -> Unit,
    onShareIos: () -> Unit,
    onScanQr: () -> Unit,
    onEnterTrial: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val balance by rememberPhoneWhiteListBalance(connected)
    val cdnView by WhiteListSelection.view.collectAsState()
    var sheet by rememberSaveable { mutableStateOf<String?>(null) }
    val cdnTags = protocols.filter { it.startsWith("cdn:") }
    val cdnSelected = selected?.startsWith("cdn:") == true
    val actual = activeProtocol ?: selected
    val serverName = actual?.let { cdnView.labels[it] ?: phoneServerLabel(it) } ?: "Автовыбор сервера"
    val daysText = when {
        !hasSubProfile -> "Войдите в аккаунт"
        daysLeft == null -> "Срок обновляется"
        daysLeft <= 0 -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит"
        else -> "Осталось $daysLeft ${daysWord(daysLeft)}"
    }
    fun openBot(cdn: Boolean = false) {
        val url = "https://t.me/MaestroSecureVPN_bot"
        if (cdn) Toast.makeText(context, "В боте откройте CDN → Купить ГБ", Toast.LENGTH_LONG).show()
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
            .onFailure { Toast.makeText(context, "Не удалось открыть Telegram", Toast.LENGTH_SHORT).show() }
    }

    BoxWithConstraints(modifier.fillMaxSize()) {
        Image(painterResource(R.drawable.phone_home_wood), null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        Image(painterResource(R.drawable.phone_home_frame), null, Modifier.fillMaxSize(), contentScale = ContentScale.FillBounds)
        val eyeSize = minOf(maxWidth - 60.dp, (maxHeight - 660.dp).coerceIn(130.dp, 260.dp))
        Column(Modifier.fillMaxSize().safeDrawingPadding().padding(horizontal = 22.dp)) {
            Column(
                Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(5.dp),
            ) {
                Box(Modifier.fillMaxWidth().height(54.dp), contentAlignment = Alignment.Center) {
                    Image(painterResource(R.drawable.phone_home_title), null, Modifier.fillMaxSize(), contentScale = ContentScale.FillBounds)
                    Text("MaestroVPN", color = PremiumGold, fontFamily = PlayfairFamily,
                        fontWeight = FontWeight.Bold, fontSize = 29.sp, maxLines = 1)
                }
                Row(horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                    PhoneHomeAction("Ввести логин", Icons.Default.Person, onEnterCode, Modifier.weight(1f))
                    PhoneHomeAction("Telegram-бот", Icons.Default.Send, { openBot() }, Modifier.weight(1f))
                }
                Row(Modifier.fillMaxWidth().fantasyFrame(R.drawable.frame_bar).padding(horizontal = 15.dp, vertical = 11.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Column(Modifier.weight(1f)) {
                        Text("VPN", color = PremiumGold, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text(daysText, color = PremiumText, fontSize = 13.sp)
                    }
                    Column(Modifier.weight(1f)) {
                        Text("CDN", color = PremiumGold, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text(balance?.removePrefix("CDN: ") ?: if (hasSubProfile) "Остаток обновляется" else "Войдите в аккаунт",
                            color = PremiumText, fontSize = 13.sp)
                    }
                }
                Box(Modifier.size(eyeSize).clip(CircleShape).clickable(role = Role.Button, onClick = onToggleConnect)
                    .semantics { contentDescription = if (connected) "Отключить VPN" else "Подключить VPN" },
                    contentAlignment = Alignment.Center) {
                    Image(painterResource(R.drawable.phone_home_ring), null, Modifier.fillMaxSize())
                    LivingEyeMedallion(connected = connected,
                        opennessOverride = if (connecting) 0.5f else if (!connected) 0f else null,
                        modifier = Modifier.fillMaxSize(0.70f))
                }
                Text(if (connecting) "Подключение…" else if (connected) "Подключено" else "Отключено",
                    color = if (connecting) Color(0xFFFFBE64) else if (connected) PremiumEmerald else Color(0xFFFF7777),
                    fontSize = 23.sp, fontWeight = FontWeight.Bold)
                Text(if (connecting) "Устанавливаем соединение" else if (connected) serverName else "Нажмите на глаз для подключения",
                    color = PremiumText, fontSize = 13.sp, textAlign = TextAlign.Center)
                Row(Modifier.fillMaxWidth().fantasyFrame(R.drawable.frame_bar).padding(6.dp),
                    horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                    PhoneMode("Обычный VPN", !cdnSelected, Modifier.weight(1f)) {
                        val auto = protocols.firstOrNull { it.equals("auto", true) || it.equals("urltest", true) }
                            ?: protocols.firstOrNull { !it.startsWith("cdn:") }
                        if (auto == null) onEnterCode() else onSelectProtocol(auto)
                    }
                    PhoneMode("CDN", cdnSelected, Modifier.weight(1f)) { sheet = "cdn" }
                }
                Row(Modifier.fillMaxWidth().fantasyFrame(R.drawable.frame_bar)
                    .clickable(role = Role.Button, onClick = onOpenServers).padding(horizontal = 15.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Default.Public, null, tint = PremiumGold, modifier = Modifier.size(26.dp))
                    Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                        Text(serverName, color = PremiumText, fontWeight = FontWeight.Bold, fontSize = 16.sp)
                        Text("Выбрать сервер", color = PremiumGold, fontSize = 12.sp)
                    }
                    Icon(Icons.Default.ChevronRight, null, tint = PremiumGold)
                }
                Row(horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                    PhoneHomeAction(if (hasSubProfile) "Продлить VPN" else "Купить VPN", Icons.Default.ShoppingCart, onBuy, Modifier.weight(1f))
                    PhoneHomeAction("Купить ГБ", Icons.Default.Add, { openBot(true) }, Modifier.weight(1f))
                }
                Text(if (cdnSelected) "CDN · расходуется пакет гигабайт" else "Обычный VPN · без расхода CDN",
                    color = PremiumGold, fontSize = 11.sp, textAlign = TextAlign.Center)
                Spacer(Modifier.height(2.dp))
            }
            Row(Modifier.fillMaxWidth().fantasyFrame(R.drawable.frame_bar).padding(vertical = 6.dp)) {
                PhoneNav("Главная", Icons.Default.Home, true, Modifier.weight(1f)) {}
                PhoneNav("Серверы", Icons.Default.Public, false, Modifier.weight(1f), onOpenServers)
                PhoneNav("Подписка", Icons.Default.AccountCircle, false, Modifier.weight(1f)) { sheet = "account" }
                PhoneNav("Настройки", Icons.Default.Settings, false, Modifier.weight(1f)) { sheet = "settings" }
            }
        }
    }
    if (sheet != null) {
        ModalBottomSheet(onDismissRequest = { sheet = null }, containerColor = PremiumWalnut, contentColor = PremiumText) {
            Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp).padding(bottom = 28.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)) {
                when (sheet) {
                    "cdn" -> {
                        Text("CDN для мобильной сети", fontSize = 23.sp, fontWeight = FontWeight.Bold, color = PremiumGold)
                        Text("Выберите сервер, чтобы подключиться. Через Wi-Fi используется обычный VPN. После возврата на мобильную сеть CDN включается вручную.")
                        balance?.let { Text(it, color = PremiumGold) }
                        if (cdnTags.isEmpty()) {
                            Text("Сейчас нет доступных CDN-серверов. Нужны мобильная сеть и активный пакет гигабайт.")
                            MobilePremiumButton("Открыть Telegram-бот", { openBot(true) }, Modifier.fillMaxWidth())
                        }
                        cdnTags.forEach { tag ->
                            MobilePremiumButton(cdnView.labels[tag] ?: "CDN", {
                                sheet = null
                                onSelectProtocol(tag)
                            }, Modifier.fillMaxWidth())
                        }
                    }
                    "account" -> {
                        Text("Моя подписка", fontSize = 23.sp, fontWeight = FontWeight.Bold, color = PremiumGold)
                        accountLogin?.let { Text("Логин: $it") }
                        Text(daysText)
                        accountExpires?.let { Text("Действует до $it") }
                        balance?.let { Text(it) }
                        MobilePremiumButton("Ввести логин", { sheet = null; onEnterCode() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Продлить VPN", { sheet = null; onBuy() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Купить гигабайты CDN", { openBot(true) }, Modifier.fillMaxWidth())
                        if (!hasSubProfile) MobilePremiumButton("Попробовать бесплатно", { sheet = null; onEnterTrial() }, Modifier.fillMaxWidth())
                    }
                    "settings" -> {
                        Text("Настройки и помощь", fontSize = 23.sp, fontWeight = FontWeight.Bold, color = PremiumGold)
                        MobilePremiumButton("Настройки приложения", { sheet = null; onOpenSettings() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Какие приложения используют VPN", { sheet = null; onSplitTunnel() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Подключить другое устройство", { sheet = null; onShareIos() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Сканировать QR-код", { sheet = null; onScanQr() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Telegram-бот", { openBot() }, Modifier.fillMaxWidth())
                        MobilePremiumButton("Написать в поддержку", {
                            runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://t.me/wapmixx"))) }
                        }, Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}

@Composable
private fun PhoneHomeAction(label: String, icon: ImageVector, onClick: () -> Unit, modifier: Modifier) {
    Column(modifier.defaultMinSize(minHeight = 52.dp).fantasyFrame(R.drawable.frame_button)
        .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 8.dp, vertical = 8.dp),
        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(19.dp))
        Text(label, color = PremiumText, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
    }
}

@Composable
private fun PhoneMode(label: String, selected: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Box(modifier.defaultMinSize(minHeight = 44.dp).clip(RoundedCornerShape(10.dp))
        .background(if (selected) PremiumEmerald.copy(alpha = 0.20f) else Color.Transparent)
        .clickable(role = Role.RadioButton, onClick = onClick).padding(8.dp), contentAlignment = Alignment.Center) {
        Text(label, color = if (selected) PremiumEmerald else PremiumGold, fontWeight = FontWeight.Bold, fontSize = 15.sp)
    }
}

@Composable
private fun PhoneNav(label: String, icon: ImageVector, active: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Column(modifier.defaultMinSize(minHeight = 50.dp).clickable(role = Role.Tab, onClick = onClick).padding(vertical = 3.dp),
        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
        val color = if (active) PremiumEmerald else PremiumGold
        Icon(icon, null, tint = color, modifier = Modifier.size(24.dp))
        Text(label, color = color, fontSize = 10.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
    }
}

private fun phoneServerLabel(tag: String): String = when (tag.lowercase()) {
    "auto", "urltest", "select" -> "Автовыбор сервера"
    "vless", "hysteria2", "anytls", "naive", "naiveproxy", "vless-s3", "vless-s4" -> "Сервер · ${tag.uppercase()}"
    else -> tag
}

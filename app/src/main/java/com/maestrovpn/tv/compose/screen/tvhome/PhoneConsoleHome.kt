package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.premium.MaestroCrown

/** Presentation only: the dashboard supplies live values and its existing action callbacks. */
@Composable
internal fun PhoneConsoleHome(
    connected: Boolean,
    connecting: Boolean,
    statusText: String,
    accountLabel: String,
    hasSubProfile: Boolean,
    daysText: String,
    balanceText: String,
    server: PhoneServer,
    cdnSelected: Boolean,
    heroWidth: Dp,
    onToggleConnect: () -> Unit,
    onEnterCode: () -> Unit,
    onBot: () -> Unit,
    onAccount: () -> Unit,
    onCdn: () -> Unit,
    onOrdinary: () -> Unit,
    onServers: () -> Unit,
    onBuy: () -> Unit,
    onBuyCdn: () -> Unit,
) {
    Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        PhoneAction(accountLabel, Icons.Default.Person, onEnterCode,
            Modifier.weight(2.2f).fillMaxHeight().testTag("phone-console-account")
                .semantics { contentDescription = if (hasSubProfile) "Аккаунт $accountLabel. Изменить логин" else "Ввести логин" },
            trailingIcon = if (hasSubProfile) Icons.Default.Edit else null, singleLine = true)
        PhoneAction("Бот", Icons.Default.Send, onBot,
            Modifier.weight(1f).fillMaxHeight().testTag("phone-console-bot"), singleLine = true)
    }
    Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        PhoneWallet("VPN", daysText, MaestroCrown,
            Modifier.weight(1f).fillMaxHeight().testTag("phone-console-vpn-wallet"), onAccount)
        PhoneWallet("CDN", balanceText, Icons.Default.Storage,
            Modifier.weight(1f).fillMaxHeight().testTag("phone-console-cdn-wallet"), onCdn)
    }
    val active = connected && !connecting
    val error = !active && !connecting && statusText.isNotBlank() && statusText != "Отключено"
    val title = when {
        connecting -> "Подключение…"
        active -> "Подключено"
        error -> "Ошибка подключения"
        else -> "Отключено"
    }
    val explanation = when {
        connecting -> "Устанавливаем соединение"
        active -> "Нажмите, чтобы отключить"
        error -> statusText
        else -> "Нажмите для подключения"
    }
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(heroWidth).testTag("phone-console-medallion")
            .semantics {
                contentDescription = if (connecting) "Отменить подключение" else if (active) "Отключить VPN" else "Подключить VPN"
                stateDescription = title
            }
            .clickable(role = Role.Button, onClick = onToggleConnect),
            contentAlignment = Alignment.Center) {
            PhoneConnectionRing(connected, connecting, Modifier.fillMaxSize())
        }
        Column(Modifier.fillMaxWidth().heightIn(min = 64.dp).testTag("phone-console-status")
            .semantics { liveRegion = LiveRegionMode.Polite }
            .padding(horizontal = 8.dp, vertical = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(title, color = if (connecting) ConsoleGoldLight else if (active) ConsoleEmerald else ConsoleCoral,
                fontFamily = FontFamily.SansSerif, fontSize = 22.sp, lineHeight = 28.sp,
                fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
            Text(explanation, color = ConsoleTextMuted, fontFamily = FontFamily.SansSerif,
                fontSize = 13.sp, lineHeight = 18.sp, textAlign = TextAlign.Center)
        }
    }
    PhoneModes(cdnSelected, onOrdinary, onCdn)
    PhoneServerRow(server, active = false, modifier = Modifier.testTag("phone-console-server"), onClick = onServers)
    Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        PhoneAction(if (hasSubProfile) "Продлить VPN" else "Купить VPN", MaestroCrown, onBuy,
            Modifier.weight(1f).fillMaxHeight().testTag("phone-console-buy-vpn"))
        PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, onBuyCdn,
            Modifier.weight(1f).fillMaxHeight().testTag("phone-console-buy-cdn"))
    }
}

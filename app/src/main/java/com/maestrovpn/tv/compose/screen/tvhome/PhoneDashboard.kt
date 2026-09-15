package com.maestrovpn.tv.compose.screen.tvhome

import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.premium.*
import com.maestrovpn.tv.whitelist.WhiteListSelection
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.update.UpdatePromptProvenance
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Phone presentation uses the approved artwork; connection and payment authorities stay existing. */
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
    entrySection: String = "home",
    onSectionChange: (String) -> Unit = {},
    onRefreshServers: () -> Unit = {},
    modifier: Modifier = Modifier,
    statusText: String = "Отключено",
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val updateChecking by UpdateState.isChecking
    var updateMessage by remember { mutableStateOf<String?>(null) }
    var refresh by remember { mutableIntStateOf(0) }
    val wallet by rememberPhoneCdnAccount(connected to refresh)
    val cdnView by WhiteListSelection.view.collectAsState()
    var page by rememberSaveable { mutableStateOf(entrySection) }
    var showCdnServers by rememberSaveable { mutableStateOf(selected?.startsWith("cdn:") == true) }
    LaunchedEffect(entrySection) { page = entrySection }
    fun navigate(target: String) { page = target; onSectionChange(target) }
    BackHandler(page != "home") { navigate("home") }
    val cdnTags = protocols.filter { it.startsWith("cdn:") }
    val ordinary = protocols.filterNot { it.startsWith("cdn:") }
    val cdnSelected = selected?.startsWith("cdn:") == true
    val actual = activeProtocol ?: selected
    val balance = wallet.balance
    val daysText = when {
        !hasSubProfile -> "Войдите в аккаунт"
        daysLeft == null -> "Срок обновляется"
        daysLeft <= 0 -> "Подписка истекла"
        daysLeft >= 3650 -> "Безлимит"
        else -> "$daysLeft ${daysWord(daysLeft)}"
    }
    val balanceText = phoneCdnBalanceText(hasSubProfile, wallet)
    fun openBot() {
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://t.me/MaestroSecureVPN_bot"))) }
            .onFailure { Toast.makeText(context, "Не удалось открыть Telegram", Toast.LENGTH_SHORT).show() }
    }
    fun openCdnPurchase() {
        val account = WhiteListSelection.account()
        if (account.first < 0) { onEnterCode(); return }
        scope.launch {
            val subscription = try {
                withContext(Dispatchers.IO) { ProfileManager.get(account.first)?.typed?.remoteURL }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) { null }
            val link = phoneCdnPurchaseLink(subscription, account, WhiteListSelection.account())
            if (link == null) {
                Toast.makeText(context, "Обновите подписку и повторите покупку для выбранного аккаунта.", Toast.LENGTH_LONG).show()
                return@launch
            }
            runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(link))) }
                .onFailure { Toast.makeText(context, "Не удалось открыть Telegram", Toast.LENGTH_SHORT).show() }
        }
    }
    fun refreshAccount() { refresh++; onRefreshServers() }
    val server = phoneServer(actual, actual?.let { cdnView.labels[it] })
    val activeTab = when (page) { "cdn", "account" -> "account"; "servers" -> "servers"; "settings", "update" -> "settings"; else -> "home" }

    PhoneConsoleLayout(activeTab, { navigate("home") }, { navigate("servers") },
        { navigate("account") }, { navigate("settings") }, modifier) { heroWidth ->
        if (page != "home") PhonePageTitle(when (page) {
            "servers" -> "Серверы"; "account" -> "Моя подписка"; "cdn" -> "CDN"; "update" -> "Обновление"; else -> "Настройки"
        }) { navigate("home") }
        when (page) {
            "home" -> PhoneConsoleHome(
                connected = connected, connecting = connecting, statusText = statusText,
                accountLabel = if (hasSubProfile) accountLogin?.takeIf { it.isNotBlank() } ?: "Аккаунт" else "Ввести логин",
                hasSubProfile = hasSubProfile, daysText = if (hasSubProfile) daysText else "—",
                balanceText = if (hasSubProfile) balanceText else "—", server = server,
                cdnSelected = cdnSelected, heroWidth = heroWidth,
                onToggleConnect = onToggleConnect, onEnterCode = onEnterCode, onBot = { openBot() },
                onAccount = { navigate("account") }, onCdn = { navigate("cdn") },
                onOrdinary = {
                    val choice = ordinary.firstOrNull { it == "auto" || it == "urltest" } ?: ordinary.firstOrNull()
                    if (choice == null) onEnterCode() else onSelectProtocol(choice)
                },
                onServers = { showCdnServers = cdnSelected; navigate("servers") },
                onBuy = onBuy, onBuyCdn = { openCdnPurchase() },
            )
            "servers" -> {
                PhoneModes(showCdnServers, { showCdnServers = false }, { showCdnServers = true })
                if (showCdnServers) PhoneText("CDN: $balanceText")
                val tags = if (showCdnServers) cdnTags else ordinary
                if (tags.isEmpty()) {
                    PhoneNotice(if (!hasSubProfile) "Войдите в аккаунт, чтобы загрузить серверы"
                        else if (showCdnServers && !cdnView.cellular) "Для CDN включите мобильную сеть"
                        else "Список серверов пока недоступен")
                }
                tags.forEach { tag -> PhoneServerRow(phoneServer(tag, cdnView.labels[tag]), selected == tag, selectable = true) { onSelectProtocol(tag) } }
                if (showCdnServers) PhoneNotice("CDN работает в мобильной сети. Для Wi-Fi выберите обычный VPN.")
                PhoneAction("Обновить список", Icons.Default.Refresh, { refreshAccount() }, Modifier.fillMaxWidth())
            }
            "account" -> {
                PhoneText(accountLogin?.let { "Логин: $it" } ?: "Войдите в свой аккаунт")
                MobilePremiumPanel {
                    PhoneText("Обычный VPN", true)
                    PhoneText(daysText)
                    accountExpires?.let { PhoneText("Действует до $it") }
                    PhoneText("Трафик без ограничений")
                    Spacer(Modifier.height(12.dp))
                    PhoneAction(if (hasSubProfile) "Продлить VPN" else "Купить VPN", MaestroCrown, onBuy, Modifier.fillMaxWidth(), true)
                }
                MobilePremiumPanel {
                    PhoneText("CDN", true)
                    PhoneText(balanceText)
                    if (wallet.unavailable && balance != null) PhoneText("Последний полученный остаток")
                    Spacer(Modifier.height(12.dp))
                    PhoneAction("Открыть CDN", Icons.Default.Storage, { navigate("cdn") }, Modifier.fillMaxWidth(), true)
                    Spacer(Modifier.height(8.dp))
                    PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, { openCdnPurchase() }, Modifier.fillMaxWidth())
                }
                PhoneAction(if (hasSubProfile) "Сменить логин" else "Ввести логин", Icons.Default.Person, onEnterCode, Modifier.fillMaxWidth())
                PhoneAction("Подключить устройство", Icons.Default.Devices, onShareIos, Modifier.fillMaxWidth())
                PhoneAction("Telegram-бот", Icons.Default.Send, { openBot() }, Modifier.fillMaxWidth())
                if (!hasSubProfile) PhoneAction("Попробовать бесплатно", Icons.Default.CardGiftcard, onEnterTrial, Modifier.fillMaxWidth())
            }
            "cdn" -> {
                val positive = balance?.primaryAccessState == "active" && balance.availableBytes > 0 &&
                    balance.publicationVerdict !in listOf("DISABLED", "PROJECTION_PENDING", "PROJECTION_STALE")
                val expired = balance?.primaryAccessState == "expired"
                MobilePremiumPanel {
                    PhoneText(when {
                        !hasSubProfile -> "Войдите в аккаунт"
                        expired -> "Продлите обычный VPN"
                        positive -> "Пакет активен"
                        wallet.loading -> "Загружаем остаток"
                        wallet.unavailable -> "Не удалось загрузить остаток"
                        balance?.publicationVerdict == "NO_BALANCE" && balance.availableBytes == 0L -> "Гигабайты закончились"
                        else -> "Обновляем данные CDN"
                    }, true)
                    if (hasSubProfile) PhoneText(balanceText)
                    if (wallet.unavailable && balance != null) PhoneText("Последний полученный остаток")
                    PhoneText(if (cdnView.cellular) "Мобильная сеть" else "Для CDN включите мобильную сеть")
                }
                when {
                    !hasSubProfile -> PhoneAction("Ввести логин", Icons.Default.Person, onEnterCode, Modifier.fillMaxWidth(), true)
                    expired -> {
                        PhoneNotice("Купленные гигабайты сохранены. Для подключения нужно продлить обычную подписку.")
                        PhoneAction("Продлить VPN", MaestroCrown, onBuy, Modifier.fillMaxWidth(), true)
                    }
                    !cdnView.cellular -> PhoneNotice("CDN отключён вне мобильной сети. Доступные гигабайты сохраняются.")
                    cdnTags.isNotEmpty() -> {
                        val target = selected?.takeIf { it in cdnTags } ?: cdnTags.first()
                        PhoneServerRow(phoneServer(target, cdnView.labels[target]), true) { showCdnServers = true; navigate("servers") }
                        PhoneAction(if (cdnView.active != null && connected && !connecting) "Отключить CDN" else if (connecting) "Подключение…" else "Подключить CDN",
                            Icons.Default.PlayArrow, {
                                if (cdnView.active != null && connected && !connecting) onToggleConnect() else if (!connecting) onSelectProtocol(target)
                            }, Modifier.fillMaxWidth(), true)
                    }
                    positive -> PhoneNotice("Не удалось загрузить CDN-серверы. Купленный пакет сохранён.")
                    else -> PhoneNotice("Серверы появятся после получения данных аккаунта и допуска к подключению.")
                }
                PhoneAction("Обновить", Icons.Default.Refresh, { refreshAccount() }, Modifier.fillMaxWidth())
                if (hasSubProfile) PhoneAction(if (positive) "Купить ещё ГБ" else "Купить ГБ", Icons.Default.ShoppingCart, { openCdnPurchase() }, Modifier.fillMaxWidth())
                PhoneNotice("При Wi-Fi CDN отключается. После возврата на мобильную сеть включите CDN вручную.")
            }
            "update" -> {
                MobilePremiumPanel {
                    PhoneText("MaestroVPN", true)
                    PhoneText("Версия ${BuildConfig.VERSION_NAME}")
                    PhoneText(updateMessage ?: "Обновления устанавливаются штатным установщиком Android.")
                }
                PhoneAction(if (updateChecking) "Проверяем…" else "Проверить обновления", Icons.Default.SystemUpdate, {
                    if (!updateChecking) scope.launch {
                        UpdateState.isChecking.value = true
                        try {
                            val info = withContext(Dispatchers.IO) { Vendor.checkUpdateAsync() }
                            if (info == null) {
                                UpdateState.applyUpdateCheckResult(Result.success(null))
                                updateMessage = "Установлена актуальная версия"
                            } else {
                                updateMessage = "Доступна версия ${info.versionName}"
                                UpdateState.requestUpdatePrompt(info, UpdatePromptProvenance.SettingsManual)
                            }
                        } catch (cancelled: CancellationException) { throw cancelled }
                        catch (_: Exception) { updateMessage = "Не удалось проверить обновление. Повторите позже." }
                        finally { UpdateState.isChecking.value = false }
                    }
                }, Modifier.fillMaxWidth(), true)
            }
            "settings" -> {
                PhoneSettingsRow("Приложения и VPN", "Выбор приложений", Icons.Default.Apps, onSplitTunnel)
                PhoneSettingsRow("Подключить устройство", "Ссылка и QR-код", Icons.Default.Devices, onShareIos)
                PhoneSettingsRow("Сканировать QR-код", null, Icons.Default.QrCodeScanner, onScanQr)
                PhoneSettingsRow("Дополнительные настройки", null, Icons.Default.Settings, onOpenSettings)
                Text("Поддержка", color = ConsoleGold, fontSize = 16.sp, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                PhoneSettingsRow("Telegram-бот", null, Icons.Default.Send, { openBot() })
                PhoneSettingsRow("Поддержка", null, Icons.Default.SupportAgent, {
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://t.me/wapmixx"))) }
                        .onFailure { Toast.makeText(context, "Не удалось открыть поддержку", Toast.LENGTH_SHORT).show() }
                })
                PhoneSettingsRow("Обновление приложения", "Проверить обновление", Icons.Default.SystemUpdate, { navigate("update") })
            }
        }
    }
}

@Composable
private fun PhonePageTitle(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().phoneConsolePanel().heightIn(min = 56.dp).padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = ConsoleGold) }
        Text(title, color = ConsoleText, fontFamily = FontFamily.SansSerif, fontSize = 20.sp,
            lineHeight = 26.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
    }
}

@Composable
private fun PhoneText(text: String, heading: Boolean = false) {
    Text(text, color = if (heading) ConsoleGoldLight else ConsoleText, fontFamily = FontFamily.SansSerif,
        fontSize = if (heading) 20.sp else 15.sp, lineHeight = if (heading) 26.sp else 20.sp,
        fontWeight = if (heading) FontWeight.SemiBold else FontWeight.Normal, modifier = Modifier.padding(vertical = 4.dp))
}

@Composable
private fun PhoneNotice(text: String) {
    Column(Modifier.fillMaxWidth().phoneConsolePanel().padding(16.dp)) { PhoneText(text) }
}

@Composable
internal fun PhoneAction(label: String, icon: ImageVector, onClick: () -> Unit, modifier: Modifier,
    active: Boolean = false, trailingIcon: ImageVector? = null, singleLine: Boolean = false) {
    Row(modifier.phoneConsolePanel(selected = active).heightIn(min = 48.dp)
        .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 12.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
        Icon(icon, null, tint = ConsoleGold, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(8.dp))
        Text(label, modifier = Modifier.weight(1f), color = ConsoleText, fontFamily = FontFamily.SansSerif,
            fontSize = 14.sp, lineHeight = 18.sp, fontWeight = FontWeight.Medium, textAlign = TextAlign.Center,
            maxLines = if (singleLine) 1 else 2, overflow = TextOverflow.Ellipsis)
        if (trailingIcon != null) {
            Spacer(Modifier.width(8.dp))
            Icon(trailingIcon, null, tint = ConsoleGold, modifier = Modifier.size(18.dp))
        }
    }
}

@Composable
internal fun PhoneWallet(label: String, value: String, icon: ImageVector, modifier: Modifier, onClick: () -> Unit) {
    Row(modifier.phoneConsolePanel().heightIn(min = 56.dp).clickable(role = Role.Button, onClick = onClick)
        .padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = ConsoleGold, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(8.dp))
        Text("$label: $value", modifier = Modifier.weight(1f), color = ConsoleText,
            fontFamily = FontFamily.SansSerif, fontSize = 14.sp, lineHeight = 18.sp,
            fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
internal fun PhoneModes(cdn: Boolean, onOrdinary: () -> Unit, onCdn: () -> Unit) {
    Row(Modifier.fillMaxWidth().testTag("phone-console-modes").phoneConsolePanel()
        .selectableGroup().padding(4.dp)) {
        listOf(Triple("Обычный VPN", Icons.Default.Shield, !cdn), Triple("CDN", Icons.Default.Storage, cdn))
            .zip(listOf(onOrdinary, onCdn)).forEach { (item, action) ->
                Row(Modifier.weight(1f).heightIn(min = 48.dp).clip(ConsoleShape)
                    .background(if (item.third) Color(0xD9185036) else Color.Transparent)
                    .selectable(selected = item.third, role = Role.RadioButton, onClick = action)
                    .padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.Center) {
                    Icon(item.second, null, tint = if (item.third) ConsoleGoldLight else ConsoleGold, modifier = Modifier.size(20.dp))
                    Spacer(Modifier.width(8.dp))
                    Text(item.first, modifier = Modifier.weight(1f, fill = false), color = ConsoleText,
                        fontFamily = FontFamily.SansSerif, fontSize = 13.sp, lineHeight = 17.sp,
                        fontWeight = if (item.third) FontWeight.SemiBold else FontWeight.Medium,
                        textAlign = TextAlign.Center, maxLines = 2)
                }
            }
    }
}

internal data class PhoneServer(val name: String, val detail: String)

/** Display labels keep the existing server-role mapping; icons are all from the same vector set. */
private fun phoneServer(tag: String?, runtimeLabel: String?): PhoneServer {
    val text = runtimeLabel ?: when (tag?.lowercase()) {
        null, "auto", "urltest", "select" -> "Автовыбор сервера"
        "vless" -> "Испания · VLESS"
        "hysteria2" -> "Чехия · Hysteria2"
        "naive", "naiveproxy", "anytls" -> "Чехия · ${tag.orEmpty().uppercase()}"
        "vless-s3" -> "Нидерланды · VLESS"
        "vless-s4" -> "Германия · VLESS"
        else -> tag.orEmpty()
    }
    val country = listOf("Испания", "Чехия", "Нидерланды", "Германия").firstOrNull { text.contains(it) }
    return PhoneServer(country ?: text,
        if (tag?.startsWith("cdn:") == true) "CDN · XHTTP" else if (country != null) text.substringAfter(" · ", "Обычный VPN") else "Обычный VPN")
}

@Composable
internal fun PhoneServerRow(server: PhoneServer, active: Boolean, selectable: Boolean = false,
    modifier: Modifier = Modifier, onClick: () -> Unit) {
    Row(modifier.fillMaxWidth().phoneConsolePanel(selected = active).heightIn(min = 64.dp)
        .clickable(role = if (selectable) Role.RadioButton else Role.Button, onClick = onClick)
        .padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Default.Public, null, tint = ConsoleGold, modifier = Modifier.size(24.dp))
        Column(Modifier.weight(1f).padding(horizontal = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(server.name, color = ConsoleText, fontFamily = FontFamily.SansSerif, fontSize = 16.sp,
                lineHeight = 21.sp, fontWeight = FontWeight.Medium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(server.detail, color = ConsoleTextMuted, fontFamily = FontFamily.SansSerif, fontSize = 12.sp,
                lineHeight = 16.sp, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        Icon(if (selectable) { if (active) Icons.Default.RadioButtonChecked else Icons.Default.RadioButtonUnchecked }
            else Icons.Default.ChevronRight, null, tint = if (active) ConsoleEmerald else ConsoleGold, modifier = Modifier.size(24.dp))
    }
}

@Composable
private fun PhoneSettingsRow(title: String, detail: String?, icon: ImageVector, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().phoneConsolePanel().heightIn(min = 56.dp)
        .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = ConsoleGold, modifier = Modifier.size(24.dp))
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(title, color = ConsoleText, fontFamily = FontFamily.SansSerif, fontSize = 15.sp, lineHeight = 20.sp)
            if (detail != null) Text(detail, color = ConsoleTextMuted, fontFamily = FontFamily.SansSerif, fontSize = 12.sp, lineHeight = 16.sp)
        }
        Icon(Icons.Default.ChevronRight, null, tint = ConsoleGold, modifier = Modifier.size(24.dp))
    }
}

@Composable
internal fun PhoneBottomNavigation(active: String, home: () -> Unit, servers: () -> Unit, account: () -> Unit, settings: () -> Unit,
    modifier: Modifier = Modifier) {
    Row(modifier.testTag("phone-console-navigation").phoneConsolePanel(navigation = true).selectableGroup().padding(4.dp)) {
        listOf(Triple("Главная", Icons.Default.Home, "home"), Triple("Серверы", Icons.Default.Public, "servers"),
            Triple("Подписка", MaestroCrown, "account"), Triple("Настройки", Icons.Default.Settings, "settings"))
            .zip(listOf(home, servers, account, settings)).forEach { (item, action) ->
                val selected = active == item.third
                val color = if (selected) ConsoleEmerald else ConsoleGold
                Column(Modifier.weight(1f).heightIn(min = 64.dp).testTag("phone-nav-${item.third}").clip(ConsoleShape)
                    .selectable(selected = selected, role = Role.Tab, onClick = action).padding(vertical = 8.dp),
                    horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Icon(item.second, null, tint = color, modifier = Modifier.size(24.dp))
                    Text(item.first, color = if (selected) ConsoleText else ConsoleTextMuted,
                        fontFamily = FontFamily.SansSerif, fontSize = 11.sp, lineHeight = 14.sp,
                        fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
                        textAlign = TextAlign.Center, maxLines = 2)
                    Box(Modifier.width(24.dp).height(2.dp).background(if (selected) ConsoleEmerald else Color.Transparent))
                }
            }
    }
}

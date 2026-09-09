package com.maestrovpn.tv.compose.screen.tvhome

import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.premium.*
import com.maestrovpn.tv.whitelist.WhiteListSelection
import java.text.DecimalFormat
import java.text.DecimalFormatSymbols
import java.util.Locale
import com.maestrovpn.tv.BuildConfig
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
    val balanceText = when {
        !hasSubProfile -> "Войдите в аккаунт"
        balance?.publicationVerdict in listOf("PROJECTION_PENDING", "PROJECTION_STALE") -> "Обновляется…"
        balance?.publicationVerdict == "DISABLED" -> "Не подключён"
        balance != null && balance.primaryAccessState == "expired" -> "${phoneGb(balance.purchasedRemainingBytes)} ГБ заморожено"
        balance?.publicationVerdict in listOf("PUBLISHABLE", "NO_BALANCE") && balance != null -> "${phoneGb(balance.availableBytes)} ГБ"
        wallet.loading -> "Загрузка…"
        else -> "Недоступен"
    }
    fun openBot() {
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://t.me/MaestroSecureVPN_bot"))) }
            .onFailure { Toast.makeText(context, "Не удалось открыть Telegram", Toast.LENGTH_SHORT).show() }
    }
    fun refreshAccount() { refresh++; onRefreshServers() }
    val server = phoneServer(actual, actual?.let { cdnView.labels[it] })
    val activeTab = when (page) { "cdn", "account" -> "account"; "servers" -> "servers"; "settings", "update" -> "settings"; else -> "home" }

    Box(modifier.fillMaxSize()) {
        ApprovedMobileBackground(Modifier.fillMaxSize())
        BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding()) {
            val side = (maxWidth * 0.085f).coerceIn(24.dp, 42.dp)
            val eyeSize = minOf(maxWidth * 0.65f, (maxHeight - 516.dp).coerceIn(160.dp, 300.dp))
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                Column(Modifier.weight(1f).fillMaxWidth().padding(horizontal = side)
                    .verticalScroll(rememberScrollState()).padding(top = 14.dp, bottom = 10.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(7.dp)) {
                    ApprovedMobileBrand(Modifier.fillMaxWidth().height(62.dp))
                    if (page != "home") PhonePageTitle(when (page) {
                        "servers" -> "Серверы"; "account" -> "Моя подписка"; "cdn" -> "CDN"; "update" -> "Обновление"; else -> "Настройки"
                    }) { navigate("home") }
                    when (page) {
                        "home" -> {
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneAction("Ввести логин", Icons.Default.Person, onEnterCode, Modifier.weight(2.85f))
                                PhoneAction("Бот", Icons.Default.Send, { openBot() }, Modifier.weight(1.15f))
                            }
                            Row(Modifier.height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneWallet("VPN", daysText, Icons.Default.Star, Modifier.weight(1f).fillMaxHeight()) { navigate("account") }
                                PhoneWallet("CDN", balanceText, Icons.Default.Storage, Modifier.weight(1f).fillMaxHeight()) { navigate("cdn") }
                            }
                            Box(Modifier.size(eyeSize).clip(CircleShape).clickable(role = Role.Button, onClick = onToggleConnect)
                                .semantics { contentDescription = if (connected && !connecting) "Отключить VPN" else "Подключить VPN" },
                                contentAlignment = Alignment.Center) {
                                ApprovedMobileEyeFrame(Modifier.fillMaxSize().clip(CircleShape))
                                LivingEyeMedallion(connected = connected && !connecting,
                                    opennessOverride = if (connecting) 0.5f else if (!connected) 0f else null,
                                    modifier = Modifier.fillMaxSize(0.76f).clip(CircleShape))
                            }
                            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
                                Text(if (connecting) "Подключение…" else if (connected) "Подключено" else "Отключено",
                                    color = if (connecting) PremiumGold else if (connected) PremiumEmerald else PremiumRuby,
                                    fontSize = 23.sp, fontWeight = FontWeight.Bold)
                                Text(if (connecting) "Устанавливаем соединение" else if (connected) "Нажмите на глаз, чтобы отключить" else "Нажмите на глаз для подключения",
                                    color = PremiumText, fontSize = 12.sp, textAlign = TextAlign.Center)
                            }
                            PhoneModes(cdnSelected, onOrdinary = {
                                val choice = ordinary.firstOrNull { it == "auto" || it == "urltest" } ?: ordinary.firstOrNull()
                                if (choice == null) onEnterCode() else onSelectProtocol(choice)
                            }, onCdn = { navigate("cdn") })
                            PhoneServerRow(server, false) { showCdnServers = cdnSelected; navigate("servers") }
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneAction(if (hasSubProfile) "Продлить VPN" else "Купить VPN", Icons.Default.Star, onBuy, Modifier.weight(1f))
                                PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, { openBot() }, Modifier.weight(1f))
                            }
                        }
                        "servers" -> {
                            PhoneModes(showCdnServers, { showCdnServers = false }, { showCdnServers = true })
                            if (showCdnServers) PhoneText("CDN: $balanceText")
                            val tags = if (showCdnServers) cdnTags else ordinary
                            if (tags.isEmpty()) {
                                PhoneNotice(if (!hasSubProfile) "Войдите в аккаунт, чтобы загрузить серверы"
                                    else if (showCdnServers && !cdnView.cellular) "Для CDN включите мобильную сеть"
                                    else "Список серверов пока недоступен")
                            }
                            tags.forEach { tag -> PhoneServerRow(phoneServer(tag, cdnView.labels[tag]), selected == tag) { onSelectProtocol(tag) } }
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
                                PhoneAction("Продлить VPN", Icons.Default.Star, onBuy, Modifier.fillMaxWidth(), true)
                            }
                            MobilePremiumPanel {
                                PhoneText("CDN", true)
                                PhoneText(balanceText)
                                if (wallet.unavailable && balance != null) PhoneText("Последний полученный остаток")
                                Spacer(Modifier.height(12.dp))
                                PhoneAction("Открыть CDN", Icons.Default.Storage, { navigate("cdn") }, Modifier.fillMaxWidth(), true)
                                Spacer(Modifier.height(7.dp))
                                PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, { openBot() }, Modifier.fillMaxWidth())
                            }
                            PhoneAction("Ввести логин", Icons.Default.Person, onEnterCode, Modifier.fillMaxWidth())
                            PhoneAction("Подключить устройство", Icons.Default.Devices, onShareIos, Modifier.fillMaxWidth())
                            PhoneAction("Telegram-бот", Icons.Default.Send, { openBot() }, Modifier.fillMaxWidth())
                            if (!hasSubProfile) PhoneAction("Попробовать бесплатно", Icons.Default.CardGiftcard, onEnterTrial, Modifier.fillMaxWidth())
                        }
                        "cdn" -> {
                            val positive = balance?.publicationVerdict == "PUBLISHABLE" && balance.availableBytes > 0
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
                                PhoneText(balanceText)
                                if (wallet.unavailable && balance != null) PhoneText("Последний полученный остаток")
                                PhoneText(if (cdnView.cellular) "Мобильная сеть" else "Для CDN включите мобильную сеть")
                            }
                            when {
                                !hasSubProfile -> PhoneAction("Ввести логин", Icons.Default.Person, onEnterCode, Modifier.fillMaxWidth(), true)
                                expired -> {
                                    PhoneNotice("Купленные гигабайты сохранены. Для подключения нужно продлить обычную подписку.")
                                    PhoneAction("Продлить VPN", Icons.Default.Star, onBuy, Modifier.fillMaxWidth(), true)
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
                            if (hasSubProfile) PhoneAction(if (positive) "Купить ещё ГБ" else "Купить ГБ", Icons.Default.ShoppingCart, { openBot() }, Modifier.fillMaxWidth())
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
                            PhoneAction("Обновление приложения", Icons.Default.SystemUpdate, { navigate("update") }, Modifier.fillMaxWidth())
                            PhoneAction("Приложения и VPN", Icons.Default.Apps, onSplitTunnel, Modifier.fillMaxWidth())
                            PhoneAction("Подключить устройство", Icons.Default.Devices, onShareIos, Modifier.fillMaxWidth())
                            PhoneAction("Сканировать QR-код", Icons.Default.QrCodeScanner, onScanQr, Modifier.fillMaxWidth())
                            PhoneAction("Дополнительные настройки", Icons.Default.Settings, onOpenSettings, Modifier.fillMaxWidth())
                            Spacer(Modifier.height(7.dp))
                            PhoneAction("Telegram-бот", Icons.Default.Send, { openBot() }, Modifier.fillMaxWidth())
                            PhoneAction("Поддержка", Icons.Default.SupportAgent, {
                                runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://t.me/wapmixx"))) }
                                    .onFailure { Toast.makeText(context, "Не удалось открыть поддержку", Toast.LENGTH_SHORT).show() }
                            }, Modifier.fillMaxWidth())
                        }
                    }
                }
                PhoneBottomNavigation(activeTab, { navigate("home") }, { navigate("servers") }, { navigate("account") }, { navigate("settings") },
                    Modifier.fillMaxWidth().padding(horizontal = 6.dp).padding(bottom = 5.dp))
            }
        }
    }
}

@Composable
private fun PhonePageTitle(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().heightIn(min = 50.dp).padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = PremiumGold) }
        Text(title, color = PremiumText, fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun PhoneText(text: String, heading: Boolean = false) {
    Text(text, color = if (heading) PremiumGold else PremiumText, fontSize = if (heading) 20.sp else 15.sp,
        fontWeight = if (heading) FontWeight.SemiBold else FontWeight.Normal, modifier = Modifier.padding(vertical = 5.dp))
}

@Composable
private fun PhoneNotice(text: String) { MobilePremiumPanel { PhoneText(text) } }

@Composable
private fun PhoneAction(label: String, icon: ImageVector, onClick: () -> Unit, modifier: Modifier, active: Boolean = false) {
    Row(modifier.approvedMobilePanel(selected = active).heightIn(min = 48.dp).clickable(role = Role.Button, onClick = onClick)
        .padding(horizontal = 12.dp, vertical = 11.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(21.dp))
        Spacer(Modifier.width(7.dp))
        Text(label, color = PremiumText, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
    }
}

@Composable
private fun PhoneWallet(label: String, value: String, icon: ImageVector, modifier: Modifier, onClick: () -> Unit) {
    Row(modifier.approvedMobilePanel().heightIn(min = 48.dp).clickable(role = Role.Button, onClick = onClick).padding(horizontal = 11.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(19.dp))
        Spacer(Modifier.width(7.dp))
        Text("$label: $value", color = PremiumText, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun PhoneModes(cdn: Boolean, onOrdinary: () -> Unit, onCdn: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().padding(4.dp), horizontalArrangement = Arrangement.spacedBy(3.dp)) {
        PhoneAction("Обычный VPN", Icons.Default.Shield, onOrdinary, Modifier.weight(1.3f), !cdn)
        PhoneAction("CDN", Icons.Default.Storage, onCdn, Modifier.weight(1f), cdn)
    }
}

private data class PhoneServer(val flag: String, val name: String, val detail: String)

/** Service roles match backend/internal/api/legacy_subscription_labels.go; unknown tags stay literal. */
private fun phoneServer(tag: String?, runtimeLabel: String?): PhoneServer {
    val text = runtimeLabel ?: when (tag?.lowercase()) {
        null, "auto", "urltest", "select" -> "Автовыбор сервера"
        "vless" -> "🇪🇸 Испания · VLESS"
        "hysteria2" -> "🇨🇿 Чехия · Hysteria2"
        "naive", "naiveproxy", "anytls" -> "🇨🇿 Чехия · ${tag.orEmpty().uppercase()}"
        "vless-s3" -> "🇳🇱 Нидерланды · VLESS"
        "vless-s4" -> "🇩🇪 Германия · VLESS"
        else -> tag.orEmpty()
    }
    val country = listOf("🇪🇸" to "Испания", "🇨🇿" to "Чехия", "🇳🇱" to "Нидерланды", "🇩🇪" to "Германия").firstOrNull { text.contains(it.second) }
    return PhoneServer(country?.first.orEmpty(), country?.second ?: text,
        if (tag?.startsWith("cdn:") == true) "CDN · XHTTP" else if (country != null) text.substringAfter(" · ", "Обычный VPN") else "Обычный VPN")
}

@Composable
private fun PhoneServerRow(server: PhoneServer, active: Boolean, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel(selected = active).heightIn(min = 58.dp)
        .clickable(role = Role.RadioButton, onClick = onClick).padding(horizontal = 14.dp, vertical = 11.dp), verticalAlignment = Alignment.CenterVertically) {
        if (server.flag.isNotEmpty()) Text(server.flag, fontSize = 25.sp) else Icon(Icons.Default.Public, null, tint = PremiumGold, modifier = Modifier.size(26.dp))
        Column(Modifier.weight(1f).padding(horizontal = 11.dp)) {
            Text(server.name, color = PremiumText, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
            Text(server.detail, color = PremiumGold, fontSize = 12.sp)
        }
        Icon(if (active) Icons.Default.CheckCircle else Icons.Default.ChevronRight, null, tint = if (active) PremiumEmerald else PremiumGold)
    }
}

@Composable
internal fun PhoneBottomNavigation(active: String, home: () -> Unit, servers: () -> Unit, account: () -> Unit, settings: () -> Unit, modifier: Modifier = Modifier) {
    Row(modifier.approvedMobilePanel(navigation = true).padding(horizontal = 13.dp, vertical = 11.dp)) {
        listOf(Triple("Главная", Icons.Default.Home, "home"), Triple("Серверы", Icons.Default.Public, "servers"),
            Triple("Подписка", Icons.Default.Star, "account"), Triple("Настройки", Icons.Default.Settings, "settings"))
            .zip(listOf(home, servers, account, settings)).forEach { (item, action) ->
                Column(Modifier.weight(1f).heightIn(min = 44.dp).clickable(role = Role.Tab, onClick = action),
                    horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    val color = if (active == item.third) PremiumEmerald else PremiumGold
                    Icon(item.second, null, tint = color, modifier = Modifier.size(23.dp))
                    Text(item.first, color = color, fontSize = 10.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
                    Box(Modifier.width(27.dp).height(2.dp).background(if (active == item.third) PremiumEmerald else Color.Transparent))
                }
            }
    }
}

private fun phoneGb(bytes: Long): String = if (bytes in 1L..9_999_999L) "< 0,01" else
    DecimalFormat("0.##", DecimalFormatSymbols(Locale("ru", "RU"))).format(java.math.BigDecimal.valueOf(bytes, 9))

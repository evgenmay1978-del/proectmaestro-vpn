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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.model.isJflServerTag
import com.maestrovpn.tv.compose.model.jflServerTags
import com.maestrovpn.tv.compose.model.pingLabel
import com.maestrovpn.tv.compose.premium.*
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.whitelist.SubXhttpRuntime
import com.maestrovpn.tv.whitelist.WhiteListSelection
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.update.UpdatePromptProvenance
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Phone presentation uses the approved artwork; connection and payment authorities stay existing. */
@Composable
internal fun PhoneDashboard(
    connected: Boolean,
    connecting: Boolean,
    protocols: List<String>,
    selected: String?,
    delays: Map<String, Int> = emptyMap(),
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
    onOpenAivpn: () -> Unit = {},
    aivpnAvailable: Boolean = false,
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
    // The "JFL" row folds the owner's external subscription into one picker with live latency.
    var jflPicker by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(entrySection) { page = entrySection }
    fun navigate(target: String) { page = target; onSectionChange(target) }
    BackHandler(page != "home") { navigate("home") }
    BackHandler(jflPicker) { jflPicker = false }
    val cdnTags = protocols.filter { it.startsWith("cdn:") }
    val jflTags = jflServerTags(protocols)
    val ordinary = protocols.filterNot { it.startsWith("cdn:") || isJflServerTag(it) }
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
        if (!context.openTelegramChat("MaestroSecureVPN_bot")) {
            Toast.makeText(context, "Не удалось открыть Telegram", Toast.LENGTH_SHORT).show()
        }
    }
    fun refreshAccount() { refresh++; onRefreshServers() }
    val server = phoneServer(actual, actual?.let { cdnView.labels[it] })
    val activeTab = when (page) { "cdn", "account" -> "account"; "servers" -> "servers"; "settings", "update" -> "settings"; else -> "home" }

    Box(modifier.fillMaxSize()) {
        ApprovedMobileBackground(Modifier.fillMaxSize())
        BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding()) {
            val side = (maxWidth * 0.085f).coerceIn(24.dp, 42.dp)
            val heroWidth = minOf(maxWidth - side * 2, (maxHeight - 460.dp).coerceIn(220.dp, 360.dp) / CARVED_MEDALLION_ASPECT)
            val heroHeight = heroWidth * CARVED_MEDALLION_ASPECT
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                Column(Modifier.weight(1f).fillMaxWidth().padding(horizontal = side)
                    .verticalScroll(rememberScrollState()).padding(bottom = 10.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(7.dp)) {
                    ApprovedMobileBrand(Modifier.fillMaxWidth().height(62.dp))
                    if (page != "home") PhonePageTitle(when (page) {
                        "servers" -> "Серверы"; "account" -> "Моя подписка"; "cdn" -> "CDN"; "update" -> "Обновление"; else -> "Настройки"
                    }) { navigate("home") }
                    when (page) {
                        "home" -> {
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneAction(if (hasSubProfile) accountLogin?.takeIf { it.isNotBlank() } ?: "Аккаунт" else "Ввести логин",
                                    Icons.Default.Person, onEnterCode, Modifier.weight(2.85f),
                                    trailingIcon = if (hasSubProfile) Icons.Default.Edit else null)
                                PhoneAction("Бот", Icons.Default.Send, { openBot() }, Modifier.weight(1.15f))
                            }
                            Row(Modifier.height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneWallet("VPN", if (hasSubProfile) daysText else "—", MaestroCrown, Modifier.weight(1f).fillMaxHeight()) { navigate("account") }
                                PhoneWallet("CDN", if (hasSubProfile) balanceText else "—", Icons.Default.Storage, Modifier.weight(1f).fillMaxHeight()) { navigate("cdn") }
                            }
                            // No spotlight behind the medallion (owner, 05.11.2026): the radial glow made
                            // the wood around the button a different tone than the rest of the page.
                            Box(Modifier.size(heroWidth, heroHeight).clickable(role = Role.Button, onClick = onToggleConnect)
                                .semantics { contentDescription = if (connected && !connecting) "Отключить VPN" else "Подключить VPN" },
                                contentAlignment = Alignment.Center) {
                                // Decorative rays/guides/spikes (owner, 07.11.2026): a vector layer UNDER the
                                // medallion, clipped above the status caption; brightness follows the real status.
                                val density = LocalDensity.current
                                var statusBlockPx by remember { mutableIntStateOf(0) }
                                with(density) {
                                    MedallionOrnament(active = connected && !connecting,
                                        medallionLeftPx = (heroWidth * CARVED_MEDALLION_LEFT).toPx(),
                                        medallionTopPx = (heroHeight * CARVED_MEDALLION_TOP).toPx(),
                                        medallionSizePx = (heroWidth * CARVED_MEDALLION_DIAMETER).toPx(),
                                        bottomLimitPx = heroHeight.toPx() - 4.dp.toPx() -
                                            (if (statusBlockPx > 0) statusBlockPx.toFloat() else 72.dp.toPx()),
                                        modifier = Modifier.fillMaxSize())
                                }
                                MonogramMedallion(connected = connected && !connecting,
                                    modifier = Modifier.align(Alignment.TopStart)
                                        .offset(x = heroWidth * CARVED_MEDALLION_LEFT, y = heroHeight * CARVED_MEDALLION_TOP)
                                        .size(heroWidth * CARVED_MEDALLION_DIAMETER))
                                // Ornament around the medallion is gone (owner, 04.11.2026): the
                                // ring with the emeralds is the asset itself, the live glow sits on it.
                                MonogramGlow(connected = connected && !connecting,
                                    modifier = Modifier.align(Alignment.TopStart)
                                        .offset(x = heroWidth * CARVED_MEDALLION_LEFT, y = heroHeight * CARVED_MEDALLION_TOP)
                                        .size(heroWidth * CARVED_MEDALLION_DIAMETER))
                            Column(modifier = Modifier.align(Alignment.BottomCenter).onSizeChanged { statusBlockPx = it.height }
                                .padding(horizontal = 5.dp, vertical = 8.dp),
                                horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
                                Text(if (connecting) "Подключение…" else if (connected) "Подключено" else "Отключено",
                                    color = if (connecting) PremiumGold else if (connected) PremiumEmerald else PremiumRuby,
                                    fontSize = 23.sp, lineHeight = 28.sp, fontWeight = FontWeight.Bold)
                                Text(if (connecting) "Устанавливаем соединение" else if (connected) "Нажмите на медальон, чтобы отключить" else "Нажмите на медальон для подключения",
                                    color = PremiumText, fontSize = 12.sp, lineHeight = 16.sp, textAlign = TextAlign.Center)
                            }
                            }
                            PhoneModes(cdnSelected, onOrdinary = {
                                val choice = ordinary.firstOrNull { it == "auto" || it == "urltest" } ?: ordinary.firstOrNull()
                                if (choice == null) onEnterCode() else onSelectProtocol(choice)
                            }, onCdn = { navigate("cdn") })
                            PhoneServerRow(server, false, ping = actual?.let { delays[it] }, tag = actual) { showCdnServers = cdnSelected; navigate("servers") }
                            // Экспериментальный протокол — на главной, рядом с выбором сервера
                            // (owner 31.10.2026). Это отдельный VpnService, а не outbound sing-box.
                            // Только аккаунтам, которым панель выдаёт ключ (/info features.aivpn):
                            // остальным строка вела бы в «Подписка отклонила запрос».
                            if (aivpnAvailable) PhoneNeuroProtocolRow(onOpenAivpn)
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                PhoneAction(if (hasSubProfile) "Продлить VPN" else "Купить VPN", MaestroCrown, onBuy, Modifier.weight(1f))
                                PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, { openBot() }, Modifier.weight(1f))
                            }
                        }
                        "servers" -> {
                            PhoneModes(showCdnServers, { showCdnServers = false }, { showCdnServers = true })
                            if (showCdnServers) PhoneText("CDN: $balanceText")
                            val tags = if (showCdnServers) cdnTags else ordinary
                            LaunchedEffect(tags) {
                                // Keep filling the latency column while the list is open: the first
                                // pass only covers a batch, and a failed probe is retried in 30 s.
                                while (true) {
                                    SubXhttpRuntime.refreshDelays(tags)
                                    delay(3_000)
                                }
                            }
                            if (tags.isEmpty()) {
                                PhoneNotice(if (!hasSubProfile) "Войдите в аккаунт, чтобы загрузить серверы"
                                    else if (showCdnServers && !cdnView.cellular) "Для CDN включите мобильную сеть"
                                    else "Список серверов пока недоступен")
                            }
                            if (!showCdnServers && jflTags.isNotEmpty()) {
                                PhoneServerRow(
                                    server = PhoneServer(
                                        flag = "🛰️",
                                        name = "JFL",
                                        detail = jflTags.size.toString() + " " + jflServerWord(jflTags.size) +
                                            " · внешняя подписка",
                                    ),
                                    active = isJflServerTag(selected),
                                    selectable = true,
                                ) { jflPicker = true }
                            }
                            tags.forEach { tag ->
                                PhoneServerRow(
                                    server = phoneServer(tag, cdnView.labels[tag]),
                                    active = selected == tag,
                                    selectable = true,
                                    ping = delays[tag],
                                    tag = tag,
                                ) { onSelectProtocol(tag) }
                            }
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
                                Spacer(Modifier.height(7.dp))
                                PhoneAction("Купить ГБ", Icons.Default.ShoppingCart, { openBot() }, Modifier.fillMaxWidth())
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
                                    PhoneServerRow(phoneServer(target, cdnView.labels[target]), true, tag = target) { showCdnServers = true; navigate("servers") }
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
                            PhoneSettingsRow("Приложения и VPN", "Выбор приложений", Icons.Default.Apps, onSplitTunnel)
                            PhoneSettingsRow("Подключить устройство", "Ссылка и QR-код", Icons.Default.Devices, onShareIos)
                            PhoneSettingsRow("Сканировать QR-код", null, Icons.Default.QrCodeScanner, onScanQr)
                            PhoneSettingsRow("Дополнительные настройки", null, Icons.Default.Settings, onOpenSettings)
                            Text("Поддержка", color = PremiumGold, fontSize = 16.sp, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                            PhoneSettingsRow("Telegram-бот", null, Icons.Default.Send, { openBot() })
                            PhoneSettingsRow("Поддержка", null, Icons.Default.SupportAgent, {
                                if (!context.openTelegramChat("wapmixx")) {
                                    Toast.makeText(context, "Не удалось открыть поддержку", Toast.LENGTH_SHORT).show()
                                }
                            })
                            PhoneSettingsRow("Обновление приложения", "Проверить обновление", Icons.Default.SystemUpdate, { navigate("update") })
                        }
                    }
                }
                PhoneBottomNavigation(activeTab, { navigate("home") }, { navigate("servers") }, { navigate("account") }, { navigate("settings") },
                    Modifier.fillMaxWidth().padding(horizontal = 15.dp).padding(bottom = 5.dp))
            }
        }

        if (jflPicker) {
            PhoneJflPicker(
                tags = jflTags,
                selected = selected,
                delays = delays,
                onSelect = { tag ->
                    jflPicker = false
                    onSelectProtocol(tag)
                },
                onDismiss = { jflPicker = false },
            )
        }
    }
}

@Composable
private fun PhonePageTitle(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().heightIn(min = 50.dp).padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = PremiumGold) }
        Text(title, color = PremiumText, fontSize = 20.sp, lineHeight = 25.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun PhoneText(text: String, heading: Boolean = false) {
    Text(text, color = if (heading) PremiumGold else PremiumText, fontSize = if (heading) 20.sp else 15.sp,
        lineHeight = if (heading) 25.sp else 20.sp,
        fontWeight = if (heading) FontWeight.SemiBold else FontWeight.Normal, modifier = Modifier.padding(vertical = 3.dp))
}

@Composable
private fun PhoneNotice(text: String) { MobilePremiumPanel { PhoneText(text) } }

@Composable
private fun PhoneAction(label: String, icon: ImageVector, onClick: () -> Unit, modifier: Modifier, active: Boolean = false, trailingIcon: ImageVector? = null) {
    Row(modifier.approvedMobilePanel(selected = active).heightIn(min = 48.dp).clickable(role = Role.Button, onClick = onClick)
        .padding(horizontal = 12.dp, vertical = 11.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(21.dp))
        Spacer(Modifier.width(7.dp))
        Text(label, modifier = Modifier.weight(1f, fill = false), color = PremiumText, fontSize = 14.sp, lineHeight = 18.sp,
            fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (trailingIcon != null) {
            Spacer(Modifier.width(7.dp))
            Icon(trailingIcon, "Сменить логин", tint = PremiumGold, modifier = Modifier.size(18.dp))
        }
    }
}

@Composable
private fun PhoneWallet(label: String, value: String, icon: ImageVector, modifier: Modifier, onClick: () -> Unit) {
    Row(modifier.approvedMobilePanel().heightIn(min = 48.dp).clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(19.dp))
        Spacer(Modifier.width(7.dp))
        Text("$label: $value", modifier = Modifier.weight(1f), color = PremiumText, fontSize = 12.sp, lineHeight = 16.sp,
            fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun PhoneModes(cdn: Boolean, onOrdinary: () -> Unit, onCdn: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().padding(horizontal = 8.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(3.dp)) {
        PhoneAction("Обычный VPN", Icons.Default.Shield, onOrdinary, Modifier.weight(1.3f), !cdn)
        PhoneAction("CDN", Icons.Default.Storage, onCdn, Modifier.weight(1f), cdn)
    }
}

private data class PhoneServer(val flag: String, val name: String, val detail: String, val icon: ImageVector = Icons.Default.Public)

/**
 * AmneziaWG приходит в подписке endpoint-ом с тегом "awg" — без флага, страны и названия, поэтому
 * без отдельной подписи строка выглядела как сырое «awg / Обычный VPN». Страна сознательно не
 * указывается: подписка её для AWG не передаёт, а сервер может переехать.
 */
internal const val AWG_SERVER_NAME = "MaestroVPN AmneziaWG"
internal const val AWG_SERVER_DETAIL = "AmneziaWG · маскировка трафика"

/** Name and subtitle of a server row, for tests. */
internal fun phoneServerTitle(tag: String?, runtimeLabel: String?): Pair<String, String> =
    phoneServer(tag, runtimeLabel).let { it.name to it.detail }

/** Service roles match backend/internal/api/legacy_subscription_labels.go; unknown tags stay literal. */
/**
 * Opens a Telegram chat. `https://t.me/<name>` alone often lands in the browser instead of the
 * app (Android 12+ opens an https link in an app only when the app's link is verified), and t.me
 * in a Russian browser is unreliable — so «Telegram-бот» did nothing useful. `tg://resolve` goes
 * straight to any installed Telegram client; the web link stays as the fallback.
 */
private fun android.content.Context.openTelegramChat(username: String): Boolean {
    val uris = listOf("tg://resolve?domain=$username", "https://t.me/$username")
    for (uri in uris) {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(uri))
        if (this !is android.app.Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (runCatching { startActivity(intent) }.isSuccess) return true
    }
    return false
}

private fun phoneServer(tag: String?, runtimeLabel: String?): PhoneServer {
    if (runtimeLabel == null && tag?.trim()?.lowercase() == "awg") {
        return PhoneServer("", AWG_SERVER_NAME, AWG_SERVER_DETAIL, Icons.Default.VpnLock)
    }
    if (runtimeLabel == null && SubXhttpRuntime.isSubXhttpTag(tag)) {
        // Subscription node that runs outside the engine (xhttp through the child Xray runner).
        // Same shape as a JFL row: flag badge, country name, and the "where it comes from" line.
        val raw = tag!!.trim()
        val flag = flagEmojiOf(raw)
        val name = stripServerDecorations(raw.removePrefix(flag)).substringAfter(" · ").trim()
        return PhoneServer(
            flag = flag,
            name = name.ifEmpty { raw },
            detail = "Внешняя подписка",
            icon = Icons.Default.Public,
        )
    }
    if (runtimeLabel == null && isJflServerTag(tag)) {
        val raw = tag!!.trim().removePrefix("JFL · ").trim()
        val flag = flagEmojiOf(raw).ifEmpty { "🛰️" }
        val name = stripServerDecorations(raw.removePrefix(flagEmojiOf(raw)))
        return PhoneServer(
            flag = flag,
            name = name.ifEmpty { raw },
            detail = "JFL · внешний сервер",
            icon = Icons.Default.Public,
        )
    }
    val raw = runtimeLabel ?: when (tag?.lowercase()) {
        null, "auto", "urltest", "select" -> "Автовыбор сервера"
        "vless" -> "🇪🇸 Испания · VLESS"
        "hysteria2" -> "🇮🇹 Италия · Hysteria2"
        "naive", "naiveproxy" -> "🇮🇹 Италия · Naive"
        "anytls" -> "🇮🇹 Италия · AnyTLS"
        "vless-s3" -> "🇳🇱 Нидерланды · VLESS"
        "vless-s4" -> "🇮🇹 Италия · VLESS"
        else -> tag.orEmpty()
    }
    // The panel prefixes whitelist routes with "БС/Yandex " (backend/internal/subgen/whitelist.go:151)
    // and the app marks them with "WL · "; both are service text, not part of the server name.
    var text = raw.removePrefix("БС/Yandex ").trim()
    val whitelist = text.startsWith("WL · ")
    if (whitelist) text = text.removePrefix("WL · ").trim()
    // Whitelist labels carry the country flag inside the label ("WL · 🇪🇪 🌀 Раю - Эстония ⚡"):
    // the badge takes the flag, the row prints the name without the panel's pictographs.
    val flag = flagEmojiOf(text)
    val clean = stripServerDecorations(text)
    val name = when {
        clean.isEmpty() -> text
        flag.isNotEmpty() || whitelist -> clean.substringBefore(" · ").trim()
        else -> clean
    }
    val detail = when {
        tag?.startsWith("cdn:") == true -> "CDN · XHTTP"
        whitelist -> "WL · внешний сервер"
        flag.isNotEmpty() -> stripServerDecorations(text.substringAfter(" · ", "")).ifEmpty { "Обычный VPN" }
        else -> "Обычный VPN"
    }
    return PhoneServer(flag, name.ifEmpty { text }, detail)
}

/**
 * Latency badge of a subscription node that runs outside sing-box. Its local SOCKS port exists only
 * while that node is the active pick, so the number is probed by SubXhttpRuntime on demand and the
 * row prints "недоступен" until a probe answers (owner's wording, 14.11.2026).
 */
@Composable
private fun subXhttpPing(tag: String?): Int? {
    val relevant = SubXhttpRuntime.isSubXhttpTag(tag)
    var value by remember(tag) { mutableStateOf(SubXhttpRuntime.cachedDelay(tag)?.toInt()) }
    LaunchedEffect(tag, relevant) {
        if (!relevant) return@LaunchedEffect
        while (true) {
            value = SubXhttpRuntime.cachedDelay(tag)?.toInt()
            delay(2_000)
        }
    }
    return value
}

@Composable
private fun PhoneServerRow(
    server: PhoneServer,
    active: Boolean,
    selectable: Boolean = false,
    ping: Int? = null,
    tag: String? = null,
    onClick: () -> Unit,
) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel(selected = active).heightIn(min = 58.dp)
        .clickable(role = Role.RadioButton, onClick = onClick).padding(horizontal = 16.dp, vertical = 11.dp), verticalAlignment = Alignment.CenterVertically) {
        PhoneServerBadge(server.flag, server.icon)
        Column(Modifier.weight(1f).padding(horizontal = 11.dp)) {
            Text(server.name, color = PremiumText, fontSize = 16.sp, lineHeight = 20.sp, fontWeight = FontWeight.SemiBold)
            Text(server.detail, color = PremiumGold, fontSize = 12.sp, lineHeight = 16.sp)
        }
        val effectivePing = ping ?: subXhttpPing(tag)
        // A row without a number must still say something (owner 14.11.2026). A subscription node
        // that failed its probe is "недоступен"; a regular node libbox has not tested yet gets a
        // neutral dash instead, because claiming it is unreachable would be a lie.
        val subXhttpRow = SubXhttpRuntime.isSubXhttpTag(tag)
        val pingText = pingLabel(effectivePing) ?: if (subXhttpRow) "недоступен" else "—"
        if (pingText != null) {
            Text(
                pingText,
                color = when {
                    effectivePing != null -> phonePingColor(effectivePing)
                    subXhttpRow -> PremiumRuby
                    else -> PremiumGold.copy(alpha = 0.55f)
                },
                fontSize = 12.sp,
                lineHeight = 16.sp,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(end = 8.dp),
            )
        }
        Icon(if (selectable) { if (active) Icons.Default.RadioButtonChecked else Icons.Default.RadioButtonUnchecked }
            else Icons.Default.ChevronRight, null, tint = if (active) PremiumEmerald else PremiumGold)
    }
}

/**
 * Country/role badge of a server row: the flag is cropped by the golden ring instead of floating
 * inside it. An emoji flag is a wide rectangle, so at the size it used to be drawn it left a visible
 * gap all around the circle (owner's screen, 04.11.2026).
 */
@Composable
private fun PhoneServerBadge(flag: String, icon: ImageVector) {
    CountryFlagBadge(flag = flag, icon = icon)
}

/** Metallic gold of the NeuroProtocol sign from the reference picture (owner, 04.11.2026). */
private val NeuroGold = Brush.verticalGradient(
    listOf(Color(0xFFF9EDC6), Color(0xFFE8C87F), Color(0xFFA8874A), Color(0xFFF3DE9F)),
)

/**
 * NeuroProtocol entry: only the brain sign and the golden lettering from the owner's picture, on the
 * same plain wood plate as every other row — no carved banner, no ornament around it.
 */
@Composable
private fun PhoneNeuroProtocolRow(onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().heightIn(min = 58.dp)
        .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(34.dp), contentAlignment = Alignment.Center) {
            Icon(Icons.Filled.Psychology, null, tint = Color(0xFF6B4A17),
                modifier = Modifier.matchParentSize().offset(y = 1.dp))
            Icon(Icons.Filled.Psychology, null, tint = PremiumGold, modifier = Modifier.matchParentSize())
        }
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(
                stringResource(R.string.aivpn_settings),
                style = TextStyle(brush = NeuroGold, fontFamily = PlayfairFamily,
                    fontWeight = FontWeight.Bold, fontSize = 21.sp, lineHeight = 25.sp),
            )
            Text(stringResource(R.string.aivpn_settings_summary), color = PremiumTextMuted,
                fontSize = 11.sp, lineHeight = 14.sp)
        }
        Icon(Icons.Default.ChevronRight, null, tint = PremiumGold, modifier = Modifier.size(19.dp))
    }
}

/** Full-screen country picker for the folded "JFL" row (phone): all external servers, live ping. */
@Composable
private fun PhoneJflPicker(
    tags: List<String>,
    selected: String?,
    delays: Map<String, Int>,
    onSelect: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    BackHandler(enabled = true) { onDismiss() }
    Box(Modifier.fillMaxSize().background(Color(0xF2080B0F))) {
        Column(Modifier.fillMaxSize().padding(16.dp)) {
            Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("JFL", color = PremiumText, fontSize = 22.sp, fontWeight = FontWeight.Bold)
                    Text(
                        tags.size.toString() + " " + jflServerWord(tags.size) + " · внешняя подписка",
                        color = PremiumGold,
                        fontSize = 12.sp,
                    )
                }
                IconButton(onClick = onDismiss) { Icon(Icons.Default.Close, "Закрыть", tint = PremiumGold) }
            }
            Column(Modifier.fillMaxWidth().weight(1f).verticalScroll(rememberScrollState())) {
                tags.forEach { tag ->
                    PhoneServerRow(
                        server = phoneServer(tag, null),
                        active = tag == selected,
                        selectable = true,
                        ping = delays[tag],
                        tag = tag,
                    ) { onSelect(tag) }
                    Spacer(Modifier.height(6.dp))
                }
            }
        }
    }
}

private fun jflServerWord(count: Int): String = when {
    count % 100 in 11..14 -> "серверов"
    count % 10 == 1 -> "сервер"
    count % 10 in 2..4 -> "сервера"
    else -> "серверов"
}

/** Same thresholds as the TV tiles and ProxyLatencyBadge. */
private fun phonePingColor(delay: Int): Color = when {
    delay in 1..99 -> PremiumEmerald
    delay in 100..299 -> PremiumGold
    else -> PremiumRuby
}

@Composable
private fun PhoneSettingsRow(title: String, detail: String?, icon: ImageVector, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().approvedMobilePanel().heightIn(min = 52.dp)
        .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = PremiumGold, modifier = Modifier.size(25.dp))
        Column(Modifier.weight(1f).padding(horizontal = 11.dp)) {
            Text(title, color = PremiumText, fontSize = 15.sp, lineHeight = 19.sp, fontWeight = FontWeight.SemiBold)
            if (detail != null) Text(detail, color = PremiumGold, fontSize = 12.sp, lineHeight = 16.sp)
        }
        Icon(Icons.Default.ChevronRight, null, tint = PremiumGold, modifier = Modifier.size(19.dp))
    }
}

@Composable
internal fun PhoneBottomNavigation(active: String, home: () -> Unit, servers: () -> Unit, account: () -> Unit, settings: () -> Unit, modifier: Modifier = Modifier) {
    // 20dp по бокам и 12dp снизу: угловые волюты и нижняя гирлянда пластины теперь фиксированного
    // размера (9-patch), и подпись/подчёркивание активной вкладки не должны на них наезжать.
    Row(modifier.approvedMobilePanel(navigation = true).padding(horizontal = 20.dp, vertical = 12.dp)) {
        listOf(Triple("Главная", Icons.Default.Home, "home"), Triple("Серверы", Icons.Default.Public, "servers"),
            Triple("Подписка", MaestroCrown, "account"), Triple("Настройки", Icons.Default.Settings, "settings"))
            .zip(listOf(home, servers, account, settings)).forEach { (item, action) ->
                Column(Modifier.weight(1f).heightIn(min = 44.dp).clickable(role = Role.Tab, onClick = action),
                    horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    val color = if (active == item.third) PremiumEmerald else PremiumGold
                    Icon(item.second, null, tint = color, modifier = Modifier.size(23.dp))
                    Text(item.first, color = color, fontSize = 10.sp, lineHeight = 14.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
                    Box(Modifier.width(27.dp).height(2.dp).background(if (active == item.third) PremiumEmerald else Color.Transparent))
                }
            }
    }
}

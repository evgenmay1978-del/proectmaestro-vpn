package com.maestrovpn.tv.compose.screen.tvhome

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.compose.model.countryNameForFlag
import com.maestrovpn.tv.compose.model.isJflServerTag
import com.maestrovpn.tv.compose.model.jflServerTags
import com.maestrovpn.tv.compose.model.serverLabel
import com.maestrovpn.tv.compose.premium.*
import com.maestrovpn.tv.compose.util.QRCodeGenerator
import com.maestrovpn.tv.update.UpdatePromptProvenance
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.vendor.Vendor
import com.maestrovpn.tv.whitelist.OwnServers
import com.maestrovpn.tv.whitelist.SubXhttpRuntime
import com.maestrovpn.tv.whitelist.WhiteListSelection
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToInt

/** Phone presentation uses the approved dark design (23.11.2026, prototype
 *  «MaestroVPN Mobile — моё видение»): clean dark screens, no wooden frame.
 *  Connection and payment authorities stay existing. */
@Composable
internal fun PhoneDashboard(
    connected: Boolean,
    connecting: Boolean,
    protocols: List<String>,
    selected: String?,
    delays: Map<String, Int> = emptyMap(),
    /** libbox outbound type per tag (groups feed / saved profile): the transport line of a row. */
    types: Map<String, String> = emptyMap(),
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    hasSubProfile: Boolean,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onOpenSettings: () -> Unit,
    onOpenAivpn: () -> Unit = {},
    aivpnAvailable: Boolean = false,
    /** The NeuroProtocol tunnel is actually carrying traffic right now. */
    aivpnActive: Boolean = false,
    /** Service start (epoch ms) for the session timer, when the sing-box service runs. */
    sessionStartMs: Long? = null,
    /** Live sing-box speeds (bytes/s), null when the service is not the transport. */
    uplinkBytesPerSec: Float? = null,
    downlinkBytesPerSec: Float? = null,
    onSplitTunnel: () -> Unit,
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
    // Selected app on the "Подключить устройство" page (MaestroVPN/Happ/Incy/Karing).
    var deviceApp by rememberSaveable { mutableStateOf(0) }
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
    val server = phoneServer(actual, actual?.let { cdnView.labels[it] }, actual?.let { types[it] })
    val activeTab = when (page) {
        "cdn", "account", "device" -> "account"
        "servers" -> "servers"
        "settings", "update" -> "settings"
        else -> "home"
    }

    // Session timer: the service start when known, otherwise the moment this screen
    // first saw the connection come up (covers the AIVPN tunnel, which has no service).
    var connectStartedAt by remember { mutableStateOf<Long?>(null) }
    LaunchedEffect(connected) {
        if (connected && connectStartedAt == null) connectStartedAt = System.currentTimeMillis()
        if (!connected) connectStartedAt = null
    }
    var nowMs by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(connected) {
        if (!connected) return@LaunchedEffect
        while (true) {
            delay(1_000)
            nowMs = System.currentTimeMillis()
        }
    }
    val sessionStart = sessionStartMs ?: connectStartedAt
    val sessionSeconds = if (connected && sessionStart != null) {
        ((nowMs - sessionStart) / 1_000).toInt().coerceAtLeast(0)
    } else {
        0
    }

    // Connect/disconnect toasts (prototype behaviour); silent on first composition.
    var wasConnected by remember { mutableStateOf<Boolean?>(null) }
    LaunchedEffect(connected) {
        val prev = wasConnected
        wasConnected = connected
        if (prev == false && connected) {
            val ping = actual?.let { delays[it] }
            val toast = if (ping != null) "Подключено: ${server.name}, $ping мс" else "Подключено: ${server.name}"
            Toast.makeText(context, toast, Toast.LENGTH_SHORT).show()
        } else if (prev == true && !connected) {
            Toast.makeText(context, "VPN отключён", Toast.LENGTH_SHORT).show()
        }
    }

    // Mode switch (prototype: segmented control; switching never disconnects).
    fun selectOrdinary() {
        // «Обычный VPN» = «Авто». A profile without it gets a third-party server,
        // our own one only when nothing else is there (owner, 15.11.2026).
        val choice = ordinary.firstOrNull { it == "auto" || it == "urltest" }
            ?: ordinary.firstOrNull { !OwnServers.isOwnTag(it) } ?: ordinary.firstOrNull()
        if (choice == null) onEnterCode() else onSelectProtocol(choice)
        Toast.makeText(context, "Режим: обычный VPN", Toast.LENGTH_SHORT).show()
    }
    fun selectCdn() {
        navigate("cdn")
        Toast.makeText(context, "Режим: CDN", Toast.LENGTH_SHORT).show()
    }

    Box(modifier.fillMaxSize()) {
        ApprovedMobileBackground(Modifier.fillMaxSize())
        BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding()) {
            val side = 18.dp
            // The coin is the hero of the approved design: ~66% of the screen width
            // (prototype stage 252 px on a 380 px phone), a plain square stage, no frame.
            val coinSize = (maxWidth * 0.66f).coerceIn(220.dp, 300.dp)
            // One scroll state per page (a shared state leaked the scroll offset
            // from one tab into the next; per-page states are remembered
            // unconditionally — never inside a when()).
            val homeScroll = rememberScrollState()
            val serversScroll = rememberScrollState()
            val accountScroll = rememberScrollState()
            val cdnScroll = rememberScrollState()
            val deviceScroll = rememberScrollState()
            val updateScroll = rememberScrollState()
            val settingsScroll = rememberScrollState()
            val scrollState = when (page) {
                "servers" -> serversScroll
                "account" -> accountScroll
                "cdn" -> cdnScroll
                "device" -> deviceScroll
                "update" -> updateScroll
                "settings" -> settingsScroll
                else -> homeScroll
            }
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                Column(
                    Modifier.weight(1f).fillMaxWidth().padding(horizontal = side)
                        .verticalScroll(scrollState)
                        .padding(bottom = 10.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    when (page) {
                        "home" -> {
                            // Header: avatar + login/plan + «Бот».
                            val username = if (hasSubProfile) {
                                accountLogin?.takeIf { it.isNotBlank() } ?: "Аккаунт"
                            } else {
                                "Ввести логин"
                            }
                            val planLine = when {
                                !hasSubProfile -> "Войдите в аккаунт"
                                daysLeft != null && daysLeft >= 3650 -> "Тариф «Безлимит»"
                                else -> "Тариф: $daysText"
                            }
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                DarkAvatar(if (hasSubProfile) username.take(1) else "M")
                                Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                                    Text(
                                        username,
                                        color = DarkText,
                                        fontSize = 16.5.sp,
                                        fontWeight = FontWeight.Bold,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                    Text(
                                        planLine,
                                        color = DarkMuted,
                                        fontSize = 12.sp,
                                        fontWeight = FontWeight.SemiBold,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                }
                                Box(
                                    Modifier
                                        .background(DarkPanel, RoundedCornerShape(999.dp))
                                        .border(1.dp, DarkLine, RoundedCornerShape(999.dp))
                                        .clickable(role = Role.Button, onClick = { openBot() })
                                        .padding(horizontal = 14.dp, vertical = 8.dp),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        Icon(Icons.Default.Send, null, tint = DarkGoldLight, modifier = Modifier.size(17.dp))
                                        Spacer(Modifier.width(6.dp))
                                        Text("Бот", color = DarkGoldLight, fontSize = 13.5.sp, fontWeight = FontWeight.Bold)
                                    }
                                }
                            }
                            // Chips: VPN / CDN остаток.
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                DarkChip(
                                    label = "VPN",
                                    value = if (hasSubProfile) daysText else "—",
                                    onClick = { navigate("account") },
                                )
                                DarkChip(
                                    label = "CDN · Остаток",
                                    value = if (hasSubProfile) balanceText else "—",
                                    goldSuffix = " ГБ",
                                    onClick = { navigate("cdn") },
                                )
                            }
                            // The coin: the whole stage is the tap target.
                            Box(
                                Modifier.size(coinSize)
                                    .clickable(role = Role.Button, onClick = onToggleConnect)
                                    .semantics {
                                        contentDescription =
                                            if (connected && !connecting) "Отключить VPN" else "Подключить VPN"
                                    },
                                contentAlignment = Alignment.Center,
                            ) {
                                MonogramMedallion(
                                    connected = connected && !connecting,
                                    connecting = connecting,
                                    modifier = Modifier.fillMaxSize(),
                                )
                                MonogramGlow(
                                    connected = connected && !connecting,
                                    modifier = Modifier.fillMaxSize(),
                                )
                            }
                            Column(
                                horizontalAlignment = Alignment.CenterHorizontally,
                                verticalArrangement = Arrangement.spacedBy(5.dp),
                            ) {
                                Text(
                                    if (connecting) "Подключение…" else if (connected) "Подключено" else "Отключено",
                                    color = if (connecting) DarkGoldLight else if (connected) DarkGreen else DarkText,
                                    fontSize = 24.sp,
                                    lineHeight = 28.sp,
                                    fontWeight = FontWeight.Bold,
                                    textAlign = TextAlign.Center,
                                )
                                Text(
                                    when {
                                        connecting -> "Выбираем маршрут и проверяем задержку"
                                        connected && cdnSelected -> "Режим CDN · Нажмите на монетку, чтобы отключить"
                                        connected -> "Нажмите на монетку, чтобы отключить"
                                        else -> "Нажмите на монетку, чтобы подключить"
                                    },
                                    color = DarkMuted,
                                    fontSize = 13.5.sp,
                                    lineHeight = 18.sp,
                                    textAlign = TextAlign.Center,
                                )
                            }
                            // Session stats (connected only).
                            if (connected && !connecting) {
                                Row(
                                    Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                                ) {
                                    DarkStatCell(
                                        value = "%02d:%02d".format(Locale.US, sessionSeconds / 60, sessionSeconds % 60),
                                        label = "Сессия",
                                        modifier = Modifier.weight(1f),
                                    )
                                    DarkStatCell(
                                        value = formatSpeed(downlinkBytesPerSec),
                                        label = "Входящий",
                                        modifier = Modifier.weight(1f),
                                    )
                                    DarkStatCell(
                                        value = formatSpeed(uplinkBytesPerSec),
                                        label = "Исходящий",
                                        modifier = Modifier.weight(1f),
                                    )
                                }
                            }
                            // Segmented VPN/CDN switch.
                            DarkSegmented(
                                options = listOf("Обычный VPN", "CDN"),
                                selectedIndex = if (cdnSelected) 1 else 0,
                                onSelect = { index -> if (index == 0) selectOrdinary() else selectCdn() },
                            )
                            // Server card.
                            val isAuto = actual == null || actual == "auto" || actual == "urltest" || actual == "select"
                            val homeRowServer = when {
                                !connected && isAuto -> PhoneServer(
                                    flag = "",
                                    name = "Автовыбор сервера",
                                    detail = "Подберёт самый быстрый автоматически",
                                )
                                else -> {
                                    val code = flagCountryCode(server.flag)
                                    val sub = if (code.isNotEmpty()) {
                                        "$code · ${server.detail} · ${if (cdnSelected) "режим CDN" else "WiFi"}"
                                    } else {
                                        "${server.detail} · ${if (cdnSelected) "режим CDN" else "WiFi"}"
                                    }
                                    server.copy(detail = sub)
                                }
                            }
                            PhoneServerRow(
                                server = homeRowServer,
                                active = false,
                                ping = actual?.let { delays[it] },
                                tag = actual,
                                autoLabel = !connected && isAuto,
                                showBadge = connected || !isAuto,
                            ) { showCdnServers = cdnSelected; navigate("servers") }
                            // NeuroProtocol card: always composed (prototype) — mounting it only
                            // when the tunnel becomes available shifted the list mid-connect and
                            // read as an overlap (owner video, r706).
                            PhoneNeuroProtocolRow(aivpnActive, onOpenAivpn)
                            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                DarkButton(
                                    if (hasSubProfile) "Продлить VPN" else "Купить VPN",
                                    onBuy,
                                    Modifier.weight(1f),
                                    primary = true,
                                    icon = MaestroCrown,
                                )
                                DarkButton("Купить ГБ", { openBot() }, Modifier.weight(1f))
                            }
                        }
                        "servers" -> {
                            DarkScreenTitle("Серверы")
                            DarkScreenSubtitle("Отсортированы по задержке. Цвет — быстрый способ понять, куда подключаться.")
                            DarkSegmented(
                                options = listOf("Обычный VPN", "CDN"),
                                selectedIndex = if (showCdnServers) 1 else 0,
                                onSelect = { index -> showCdnServers = index == 1 },
                            )
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
                                Box(
                                    Modifier.fillMaxWidth().darkCard().padding(horizontal = 16.dp, vertical = 14.dp),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    Text(
                                        if (!hasSubProfile) "Войдите в аккаунт, чтобы загрузить серверы"
                                        else if (showCdnServers && !cdnView.cellular) "Для CDN включите мобильную сеть"
                                        else "Список серверов пока недоступен",
                                        color = DarkMuted,
                                        fontSize = 13.5.sp,
                                        textAlign = TextAlign.Center,
                                    )
                                }
                            }
                            val autoSelected = selected == null || selected == "auto" || selected == "urltest" || selected == "select"
                            if (!showCdnServers) {
                                // «Автовыбор» — как в прототипе, с лучшим сервером по текущей задержке.
                                val bestTag = ordinary.minByOrNull { delays[it] ?: Int.MAX_VALUE }
                                val bestServer = bestTag?.let { phoneServer(it, cdnView.labels[it], types[it]) }
                                val bestPing = bestTag?.let { delays[it] }
                                Row(
                                    Modifier.fillMaxWidth().approvedMobilePanel(selected = autoSelected)
                                        .heightIn(min = 58.dp)
                                        .clickable(role = Role.RadioButton) {
                                            val choice = ordinary.firstOrNull { it == "auto" || it == "urltest" }
                                                ?: ordinary.firstOrNull { !OwnServers.isOwnTag(it) }
                                                ?: ordinary.firstOrNull()
                                            if (choice != null) onSelectProtocol(choice)
                                        }
                                        .padding(horizontal = 16.dp, vertical = 11.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    DarkIconTile(Icons.Default.Public)
                                    Column(Modifier.weight(1f).padding(horizontal = 11.dp)) {
                                        Text(
                                            "Автовыбор сервера",
                                            color = DarkText,
                                            fontSize = 15.sp,
                                            fontWeight = FontWeight.Bold,
                                            maxLines = 1,
                                        )
                                        Text(
                                            if (bestServer != null && bestPing != null) {
                                                "Сейчас лучший: ${bestServer.name} · $bestPing мс"
                                            } else {
                                                "Подберёт самый быстрый автоматически"
                                            },
                                            color = DarkMuted,
                                            fontSize = 12.sp,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                    }
                                    Icon(
                                        if (autoSelected) Icons.Default.RadioButtonChecked else Icons.Default.RadioButtonUnchecked,
                                        null,
                                        tint = if (autoSelected) DarkGreen else DarkGoldLight,
                                        modifier = Modifier.size(22.dp),
                                    )
                                }
                            }
                            if (!showCdnServers && jflTags.isNotEmpty()) {
                                PhoneServerRow(
                                    server = PhoneServer(
                                        // The folded row has no country: the globe badge, like every
                                        // other server row without a flag.
                                        flag = "",
                                        name = "JFL",
                                        detail = jflTags.size.toString() + " " + jflServerWord(jflTags.size),
                                    ),
                                    active = isJflServerTag(selected),
                                    selectable = true,
                                ) { jflPicker = true }
                            }
                            // Grouped by country (owner, 23.11.2026): rows with a flag go under
                            // their country name, the rest under «Другие»; fastest first in a group.
                            val grouped = tags.groupBy { tag ->
                                val row = phoneServer(tag, cdnView.labels[tag], types[tag])
                                if (row.flag.isNotEmpty()) row.name else "Другие"
                            }
                            grouped.entries.sortedWith(compareBy({ it.key == "Другие" }, { it.key })).forEach { (group, groupTags) ->
                                DarkGroupLabel(group)
                                groupTags.sortedBy { delays[it] ?: Int.MAX_VALUE }.forEach { tag ->
                                    PhoneServerRow(
                                        server = phoneServer(tag, cdnView.labels[tag], types[tag]),
                                        active = selected == tag,
                                        selectable = true,
                                        ping = delays[tag],
                                        tag = tag,
                                    ) { onSelectProtocol(tag) }
                                }
                            }
                            if (showCdnServers) {
                                Text(
                                    "CDN работает в мобильной сети. Для Wi-Fi выберите обычный VPN.",
                                    color = DarkFaint,
                                    fontSize = 12.5.sp,
                                    textAlign = TextAlign.Center,
                                    modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                                )
                            }
                            DarkButton(
                                "Обновить список",
                                { refreshAccount() },
                                Modifier.fillMaxWidth(),
                                icon = Icons.Default.Refresh,
                            )
                        }
                        "account" -> {
                            DarkScreenTitle("Моя подписка")
                            DarkScreenSubtitle("Всё об аккаунте — на одном экране, ровными строками.")
                            val lifetime = daysLeft != null && daysLeft >= 3650
                            // Карточка тарифа.
                            Column(
                                Modifier.fillMaxWidth().darkCard().padding(20.dp),
                                verticalArrangement = Arrangement.spacedBy(10.dp),
                            ) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(
                                        if (lifetime) "Безлимит" else "VPN",
                                        color = DarkGoldLight,
                                        fontSize = 22.sp,
                                        fontWeight = FontWeight.Bold,
                                        modifier = Modifier.weight(1f),
                                    )
                                    if (hasSubProfile) {
                                        DarkBadge(
                                            if (daysLeft != null && daysLeft <= 0) "Истекла" else "Активна",
                                            green = daysLeft == null || daysLeft > 0,
                                        )
                                    }
                                }
                                Text(
                                    if (lifetime) {
                                        "VPN без ограничений по трафику и сроку действия. Никаких дат из следующего века — безлимит так и подписан: бессрочно."
                                    } else {
                                        "VPN без ограничений по трафику."
                                    },
                                    color = DarkMuted,
                                    fontSize = 14.sp,
                                    lineHeight = 20.sp,
                                )
                            }
                            // Карточка «ключ — значение»: срок действия — «Бессрочно», а не 28.02.2109.
                            val kvRows = listOf(
                                "Тариф" to if (lifetime) "Безлимит" else "VPN",
                                "Срок действия" to when {
                                    !hasSubProfile -> "—"
                                    lifetime -> "Бессрочно"
                                    accountExpires != null -> accountExpires
                                    daysLeft != null -> "Осталось $daysText"
                                    else -> "—"
                                },
                                "Трафик" to "Без ограничений",
                                "Логин" to (accountLogin?.takeIf { it.isNotBlank() } ?: "—"),
                            )
                            Column(
                                Modifier.fillMaxWidth().darkCard().padding(horizontal = 20.dp, vertical = 6.dp),
                            ) {
                                kvRows.forEachIndexed { index, (key, value) ->
                                    Row(
                                        Modifier.fillMaxWidth().padding(vertical = 10.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Text(key, color = DarkMuted, fontSize = 14.sp, modifier = Modifier.weight(1f))
                                        Text(
                                            value,
                                            color = DarkText,
                                            fontSize = 14.sp,
                                            fontWeight = FontWeight.Bold,
                                            textAlign = TextAlign.End,
                                        )
                                    }
                                    if (index < kvRows.lastIndex) DarkMenuDivider()
                                }
                            }
                            // Карточка CDN.
                            Column(
                                Modifier.fillMaxWidth().darkCard().padding(horizontal = 20.dp, vertical = 16.dp),
                                verticalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(
                                        "CDN",
                                        color = DarkText,
                                        fontSize = 16.sp,
                                        fontWeight = FontWeight.Bold,
                                        modifier = Modifier.weight(1f),
                                    )
                                    Text("Остаток на балансе", color = DarkMuted, fontSize = 12.sp)
                                }
                                Text(
                                    text = androidx.compose.ui.text.AnnotatedString.Builder().apply {
                                        val main = if (balanceText.endsWith(" ГБ")) {
                                            balanceText.removeSuffix(" ГБ")
                                        } else {
                                            balanceText
                                        }
                                        append(main)
                                        if (balanceText.endsWith(" ГБ")) {
                                            withStyle(
                                                androidx.compose.ui.text.SpanStyle(color = DarkGoldLight),
                                            ) { append(" ГБ") }
                                        }
                                    }.toAnnotatedString(),
                                    color = DarkText,
                                    fontSize = 21.sp,
                                    fontWeight = FontWeight.Bold,
                                )
                            }
                            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                DarkButton(
                                    if (hasSubProfile) "Продлить VPN" else "Купить VPN",
                                    onBuy,
                                    Modifier.weight(1f),
                                    primary = true,
                                    icon = MaestroCrown,
                                )
                                DarkButton("Купить ГБ", { openBot() }, Modifier.weight(1f))
                            }
                            DarkGroupLabel("Аккаунт")
                            DarkMenuList {
                                DarkMenuRow(
                                    title = "Сменить логин",
                                    icon = Icons.Default.Edit,
                                    onClick = onEnterCode,
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Подключить устройство",
                                    icon = Icons.Default.Devices,
                                    onClick = { navigate("device") },
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Telegram-бот",
                                    icon = Icons.Default.Send,
                                    onClick = { openBot() },
                                    showChevron = true,
                                )
                            }
                            if (!hasSubProfile) {
                                DarkButton(
                                    "Попробовать бесплатно",
                                    onEnterTrial,
                                    Modifier.fillMaxWidth(),
                                    icon = Icons.Default.CardGiftcard,
                                )
                            }
                        }
                        "cdn" -> {
                            PhonePageTitle("CDN") { navigate("home") }
                            val positive = balance?.primaryAccessState == "active" && balance.availableBytes > 0 &&
                                balance.publicationVerdict !in listOf("DISABLED", "PROJECTION_PENDING", "PROJECTION_STALE")
                            val expired = balance?.primaryAccessState == "expired"
                            Column(
                                Modifier.fillMaxWidth().darkCard().padding(horizontal = 20.dp, vertical = 16.dp),
                                verticalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                Text(
                                    when {
                                        !hasSubProfile -> "Войдите в аккаунт"
                                        expired -> "Продлите обычный VPN"
                                        positive -> "Пакет активен"
                                        wallet.loading -> "Загружаем остаток"
                                        wallet.unavailable -> "Не удалось загрузить остаток"
                                        balance?.publicationVerdict == "NO_BALANCE" && balance.availableBytes == 0L -> "Гигабайты закончились"
                                        else -> "Обновляем данные CDN"
                                    },
                                    color = DarkText,
                                    fontSize = 16.sp,
                                    fontWeight = FontWeight.Bold,
                                )
                                if (hasSubProfile) Text(balanceText, color = DarkMuted, fontSize = 14.sp)
                                if (wallet.unavailable && balance != null) {
                                    Text("Последний полученный остаток", color = DarkMuted, fontSize = 13.sp)
                                }
                                Text(
                                    if (cdnView.cellular) "Мобильная сеть" else "Для CDN включите мобильную сеть",
                                    color = DarkMuted,
                                    fontSize = 13.sp,
                                )
                            }
                            when {
                                !hasSubProfile -> DarkButton(
                                    "Ввести логин",
                                    onEnterCode,
                                    Modifier.fillMaxWidth(),
                                    primary = true,
                                    icon = Icons.Default.Person,
                                )
                                expired -> {
                                    DarkNotice("Купленные гигабайты сохранены. Для подключения нужно продлить обычную подписку.")
                                    DarkButton(
                                        "Продлить VPN",
                                        onBuy,
                                        Modifier.fillMaxWidth(),
                                        primary = true,
                                        icon = MaestroCrown,
                                    )
                                }
                                !cdnView.cellular -> DarkNotice("CDN отключён вне мобильной сети. Доступные гигабайты сохраняются.")
                                cdnTags.isNotEmpty() -> {
                                    val target = selected?.takeIf { it in cdnTags } ?: cdnTags.first()
                                    PhoneServerRow(
                                        phoneServer(target, cdnView.labels[target]),
                                        true,
                                        tag = target,
                                    ) { showCdnServers = true; navigate("servers") }
                                    DarkButton(
                                        if (cdnView.active != null && connected && !connecting) "Отключить CDN"
                                        else if (connecting) "Подключение…" else "Подключить CDN",
                                        {
                                            if (cdnView.active != null && connected && !connecting) onToggleConnect()
                                            else if (!connecting) onSelectProtocol(target)
                                        },
                                        Modifier.fillMaxWidth(),
                                        primary = true,
                                        icon = Icons.Default.PlayArrow,
                                    )
                                }
                                positive -> DarkNotice("Не удалось загрузить CDN-серверы. Купленный пакет сохранён.")
                                else -> DarkNotice("Серверы появятся после получения данных аккаунта и допуска к подключению.")
                            }
                            DarkButton(
                                "Обновить",
                                { refreshAccount() },
                                Modifier.fillMaxWidth(),
                                icon = Icons.Default.Refresh,
                            )
                            if (hasSubProfile) {
                                DarkButton(
                                    if (positive) "Купить ещё ГБ" else "Купить ГБ",
                                    { openBot() },
                                    Modifier.fillMaxWidth(),
                                    icon = Icons.Default.ShoppingCart,
                                )
                            }
                            Text(
                                "При Wi-Fi CDN отключается. После возврата на мобильную сеть включите CDN вручную.",
                                color = DarkFaint,
                                fontSize = 12.5.sp,
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                            )
                        }
                        "update" -> {
                            PhonePageTitle("Обновление") { navigate("home") }
                            Column(
                                Modifier.fillMaxWidth().darkCard().padding(horizontal = 20.dp, vertical = 16.dp),
                                verticalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                Text("MaestroVPN", color = DarkText, fontSize = 16.sp, fontWeight = FontWeight.Bold)
                                Text("Версия ${BuildConfig.VERSION_NAME}", color = DarkMuted, fontSize = 14.sp)
                                Text(
                                    updateMessage ?: "Обновления устанавливаются штатным установщиком Android.",
                                    color = DarkMuted,
                                    fontSize = 14.sp,
                                )
                            }
                            DarkButton(
                                if (updateChecking) "Проверяем…" else "Проверить обновления",
                                {
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
                                },
                                Modifier.fillMaxWidth(),
                                primary = true,
                                icon = Icons.Default.SystemUpdate,
                            )
                        }
                        "settings" -> {
                            DarkScreenTitle("Настройки")
                            Column(
                                Modifier.fillMaxWidth().darkCard()
                                    .clickable(role = Role.Button) { navigate("account") }
                                    .padding(horizontal = 16.dp, vertical = 14.dp),
                            ) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    DarkAvatar((accountLogin?.takeIf { it.isNotBlank() } ?: "M").take(1))
                                    Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                                        Text(
                                            if (hasSubProfile) accountLogin ?: "Аккаунт" else "Ввести логин",
                                            color = DarkText,
                                            fontSize = 16.sp,
                                            fontWeight = FontWeight.Bold,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                        Text(
                                            when {
                                                !hasSubProfile -> "Войдите в аккаунт"
                                                daysLeft != null && daysLeft >= 3650 -> "Безлимит · бессрочно"
                                                else -> daysText
                                            },
                                            color = DarkMuted,
                                            fontSize = 12.5.sp,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                    }
                                    Icon(Icons.Default.ChevronRight, null, tint = DarkFaint, modifier = Modifier.size(19.dp))
                                }
                            }
                            DarkGroupLabel("Подключение")
                            DarkMenuList {
                                DarkMenuRow(
                                    title = "Приложения и VPN",
                                    sub = "Выбор приложений",
                                    icon = Icons.Default.Apps,
                                    onClick = onSplitTunnel,
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Подключить устройство",
                                    sub = "Ссылка и QR-код",
                                    icon = Icons.Default.Devices,
                                    onClick = { navigate("device") },
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Сканировать QR-код",
                                    icon = Icons.Default.QrCodeScanner,
                                    onClick = onScanQr,
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Протокол по умолчанию",
                                    value = "Авто",
                                    icon = Icons.Default.Public,
                                    onClick = {
                                        Toast.makeText(context, "Выбор протокола: сейчас «Авто»", Toast.LENGTH_SHORT).show()
                                    },
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Дополнительные настройки",
                                    icon = Icons.Default.Settings,
                                    onClick = onOpenSettings,
                                    showChevron = true,
                                )
                            }
                            DarkGroupLabel("Приложение")
                            DarkMenuList {
                                DarkMenuRow(
                                    title = "Язык",
                                    value = "Русский",
                                    icon = Icons.Default.Language,
                                    onClick = {
                                        Toast.makeText(context, "Язык интерфейса: русский", Toast.LENGTH_SHORT).show()
                                    },
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Оформление",
                                    value = "Тёмное",
                                    icon = Icons.Default.DarkMode,
                                    onClick = {
                                        Toast.makeText(context, "Оформление: тёмное, как всегда", Toast.LENGTH_SHORT).show()
                                    },
                                )
                            }
                            DarkGroupLabel("О приложении")
                            DarkMenuList {
                                DarkMenuRow(
                                    title = "Проверить обновления",
                                    sub = "Проверить обновление",
                                    icon = Icons.Default.SystemUpdate,
                                    onClick = { navigate("update") },
                                    showChevron = true,
                                )
                                DarkMenuDivider()
                                DarkMenuRow(
                                    title = "Поддержка",
                                    icon = Icons.Default.SupportAgent,
                                    onClick = {
                                        if (!context.openTelegramChat("wapmixx")) {
                                            Toast.makeText(context, "Не удалось открыть поддержку", Toast.LENGTH_SHORT).show()
                                        }
                                    },
                                    showChevron = true,
                                )
                            }
                            DarkVersionLine("MaestroVPN · версия ${BuildConfig.VERSION_NAME}")
                        }
                        "device" -> {
                            DarkScreenTitle("Подключить устройство")
                            DarkScreenSubtitle("Отсканируйте код камерой второго устройства или откройте ссылку в одном из приложений.")
                            when (val state = rememberShareState()) {
                                is ShareState.Loading -> DarkNotice("Загрузка…")
                                is ShareState.NeedActivate -> DarkNotice("Сначала войдите в аккаунт MaestroVPN.")
                                is ShareState.Failed -> DarkNotice("Не удалось получить подписку. Закройте страницу и повторите.")
                                is ShareState.Ready -> {
                                    val url = shareUrlForApp(state.baseUrl, deviceApp)
                                    val qr = remember(url) {
                                        runCatching {
                                            QRCodeGenerator.generate(
                                                url,
                                                640,
                                                android.graphics.Color.BLACK,
                                                android.graphics.Color.WHITE,
                                            )
                                        }.getOrNull()
                                    }
                                    Column(
                                        Modifier.fillMaxWidth().darkCard().padding(20.dp),
                                        horizontalAlignment = Alignment.CenterHorizontally,
                                        verticalArrangement = Arrangement.spacedBy(12.dp),
                                    ) {
                                        Box(
                                            Modifier.background(Color.White, RoundedCornerShape(16.dp)).padding(10.dp),
                                        ) {
                                            if (qr != null) {
                                                Image(qr.asImageBitmap(), null, modifier = Modifier.size(188.dp))
                                            } else {
                                                Text(
                                                    "Не удалось построить QR-код",
                                                    color = DarkFaint,
                                                    fontSize = 13.sp,
                                                    modifier = Modifier.size(188.dp),
                                                    textAlign = TextAlign.Center,
                                                )
                                            }
                                        }
                                        Text(
                                            "Код содержит вашу ссылку подписки.\nНе делитесь им с посторонними.",
                                            color = DarkMuted,
                                            fontSize = 13.sp,
                                            lineHeight = 18.sp,
                                            textAlign = TextAlign.Center,
                                        )
                                    }
                                    Row(
                                        Modifier.fillMaxWidth().darkCard().padding(horizontal = 16.dp, vertical = 12.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Text(
                                            maskSubUrl(url),
                                            color = DarkText,
                                            fontSize = 13.5.sp,
                                            modifier = Modifier.weight(1f),
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                        Spacer(Modifier.width(10.dp))
                                        DarkButton("Копировать", onClick = {
                                            val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                                            clipboard.setPrimaryClip(ClipData.newPlainText("MaestroVPN", url))
                                            Toast.makeText(context, "Ссылка подписки скопирована", Toast.LENGTH_SHORT).show()
                                        })
                                    }
                                    val apps = listOf("MaestroVPN", "Happ", "Incy", "Karing")
                                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                                        apps.chunked(2).forEachIndexed { rowIndex, rowApps ->
                                            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                                rowApps.forEachIndexed { colIndex, app ->
                                                    val index = rowIndex * 2 + colIndex
                                                    val appSelected = deviceApp == index
                                                    Box(
                                                        Modifier.weight(1f).heightIn(min = 54.dp)
                                                            .background(DarkPanel, RoundedCornerShape(DarkPanelCorner))
                                                            .border(
                                                                1.dp,
                                                                if (appSelected) DarkGold else DarkLineSoft,
                                                                RoundedCornerShape(DarkPanelCorner),
                                                            )
                                                            .clickable(role = Role.Button) {
                                                                deviceApp = index
                                                                Toast.makeText(context, "Открываю $app…", Toast.LENGTH_SHORT).show()
                                                            }
                                                            .padding(horizontal = 14.dp, vertical = 12.dp),
                                                        contentAlignment = Alignment.Center,
                                                    ) {
                                                        Row(verticalAlignment = Alignment.CenterVertically) {
                                                            Box(
                                                                Modifier.size(9.dp)
                                                                    .background(
                                                                        if (appSelected) DarkGold else DarkFaint,
                                                                        CircleShape,
                                                                    ),
                                                            )
                                                            Spacer(Modifier.width(8.dp))
                                                            Text(
                                                                app,
                                                                color = if (appSelected) DarkGoldLight else DarkText,
                                                                fontSize = 15.sp,
                                                                fontWeight = FontWeight.Bold,
                                                            )
                                                        }
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }
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
                types = types,
                onSelect = { tag ->
                    jflPicker = false
                    onSelectProtocol(tag)
                },
                onDismiss = { jflPicker = false },
            )
        }
    }
}

private data class PhoneServer(val flag: String, val name: String, val detail: String, val icon: ImageVector = Icons.Default.Public)

/** Name and subtitle of a server row, for tests. */
internal fun phoneServerTitle(tag: String?, runtimeLabel: String?): Pair<String, String> =
    phoneServer(tag, runtimeLabel).let { it.name to it.detail }

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

private fun phoneServer(tag: String?, runtimeLabel: String?, type: String? = null): PhoneServer {
    // A server that names itself (country flag or provider prefix — JFL, WL, xhttp nodes, our
    // relabeled nodes) gets the same flag/name/transport as on every other screen (ServerLabel).
    if (runtimeLabel == null) {
        serverLabel(tag, type, SubXhttpRuntime.isSubXhttpTag(tag))?.let { label ->
            return PhoneServer(label.flag, label.name, label.detail)
        }
    }
    val raw = runtimeLabel ?: when (tag?.lowercase()) {
        null, "auto", "urltest", "select" -> "Автовыбор сервера"
        "vless" -> "🇪🇸 Испания · VLESS"
        "hysteria2" -> "🇮🇹 Италия · Hysteria2"
        "vless-s3" -> "🇳🇱 Нидерланды · VLESS"
        "vless-s4" -> "🇮🇹 Италия · VLESS"
        else -> tag.orEmpty()
    }
    // The panel prefixes whitelist routes with "БС/Yandex " (backend/internal/subgen/whitelist.go:151);
    // it is service text, not part of the server name.
    val text = raw.removePrefix("БС/Yandex ").trim()
    // A CDN label may carry the country flag inside it: the badge takes the flag, the row prints
    // the name without the panel's pictographs.
    val flag = flagEmojiOf(text)
    val clean = stripServerDecorations(text)
    val segments = clean.split(" · ", " – ", " - ").map { it.trim() }.filter { it.isNotEmpty() }
    val country = if (flag.isNotEmpty()) countryNameForFlag(flag) else null
    // The flag is the country: a provider brand before it ("Яук – Германия",
    // "Вьюга - Швейцария", "Akonit · Финляндия") is dropped, not printed as the
    // server name (owner video, r706). The same rule lives in serverLabel() for the
    // no-label path; both paths must agree.
    val name = when {
        clean.isEmpty() -> text
        country != null && segments.contains(country) -> country
        flag.isNotEmpty() -> clean.substringBefore(" · ").trim()
        else -> clean
    }
    val detail = when {
        tag?.startsWith("cdn:") == true -> "CDN · XHTTP"
        flag.isNotEmpty() -> {
            if (country != null) {
                // The part after the country names the transport/tag ("VLESS", "Torrent");
                // a bare "Brand – Country" label has none.
                segments.drop(segments.indexOf(country) + 1).joinToString(" · ").ifEmpty { "Обычный VPN" }
            } else {
                stripServerDecorations(text.substringAfter(" · ", "")).ifEmpty { "Обычный VPN" }
            }
        }
        else -> "Обычный VPN"
    }
    return PhoneServer(flag, name.ifEmpty { text }, detail)
}

/**
 * Latency of a subscription node that runs outside sing-box: the number, plus whether a probe has
 * answered at all. Both parts matter — a row cannot tell "probe still running" from "probe failed"
 * with the number alone, and "недоступен" printed before the answer is the same kind of lie as a
 * green ping on a dead node (owner's report, 14.11.2026).
 */
private class SubXhttpPing(val ms: Int?, val attempted: Boolean)

/**
 * Latency badge of a subscription node that runs outside sing-box. Its local SOCKS port exists only
 * while that node is the active pick, so the number is probed by SubXhttpRuntime on demand.
 */
@Composable
private fun subXhttpPing(tag: String?): SubXhttpPing {
    val relevant = SubXhttpRuntime.isSubXhttpTag(tag)
    var value by remember(tag) { mutableStateOf(SubXhttpRuntime.cachedDelay(tag)?.toInt()) }
    var attempted by remember(tag) { mutableStateOf(SubXhttpRuntime.probeAttempted(tag)) }
    LaunchedEffect(tag, relevant) {
        if (!relevant) return@LaunchedEffect
        while (true) {
            value = SubXhttpRuntime.cachedDelay(tag)?.toInt()
            attempted = SubXhttpRuntime.probeAttempted(tag)
            delay(2_000)
        }
    }
    return SubXhttpPing(value, attempted)
}

/** Back title of the hidden phone pages (CDN, update). Tab pages use DarkScreenTitle instead. */
@Composable
private fun PhonePageTitle(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onBack, modifier = Modifier.size(40.dp)) {
            Icon(Icons.Default.ArrowBack, "Назад", tint = DarkGoldLight)
        }
        Text(title, color = DarkText, fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
    }
}

/** Muted notice card (replaces the old PhoneNotice panel). */
@Composable
private fun DarkNotice(text: String, modifier: Modifier = Modifier) {
    Box(
        modifier.fillMaxWidth().darkCard().padding(horizontal = 16.dp, vertical = 14.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, color = DarkMuted, fontSize = 13.5.sp, lineHeight = 19.sp, textAlign = TextAlign.Center)
    }
}

@Composable
private fun PhoneServerRow(
    server: PhoneServer,
    active: Boolean,
    selectable: Boolean = false,
    ping: Int? = null,
    tag: String? = null,
    /** Home card, disconnected auto mode: the latency slot reads "Авто" (prototype). */
    autoLabel: Boolean = false,
    /** The home auto card hides the flag slot until a server is chosen (prototype). */
    showBadge: Boolean = true,
    onClick: () -> Unit,
) {
    // A row without a number must still say something (owner 14.11.2026). A subscription node
    // whose probe FAILED is "недоступен"; one whose probe is still running gets a neutral dash,
    // because "недоступен" before the answer is the same lie as a green ping on a dead node.
    val subXhttpRow = SubXhttpRuntime.isSubXhttpTag(tag)
    val probed = if (subXhttpRow) subXhttpPing(tag) else SubXhttpPing(null, false)
    val effectivePing = ping ?: probed.ms
    // The engine re-probes every few seconds, so the raw number jumps (177/165/185 мс in the
    // owner's r706 video). The row shows an EMA of the probes and repaints only when it moved
    // by at least 5 мс — the number stays readable instead of flickering.
    var shownPing by remember(tag) { mutableStateOf(effectivePing) }
    LaunchedEffect(effectivePing) {
        val raw = effectivePing ?: return@LaunchedEffect
        val current = shownPing
        val next = if (current == null) raw else ((current * 0.7f + raw * 0.3f).roundToInt())
        if (current == null || abs(next - current) >= 5) shownPing = next
    }
    // An unavailable node is dimmed as well as captioned (owner, 23.11.2026).
    val unavailable = subXhttpRow && probed.attempted && effectivePing == null
    val pingText = when {
        autoLabel -> "Авто"
        unavailable -> "недоступен"
        shownPing != null -> "$shownPing мс"
        else -> "—"
    }
    Row(
        Modifier.fillMaxWidth().approvedMobilePanel(selected = active).heightIn(min = 58.dp)
            .alpha(if (unavailable) 0.48f else 1f)
            .clickable(role = Role.RadioButton, onClick = onClick).padding(horizontal = 16.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (showBadge) PhoneServerBadge(server.flag, server.icon)
        Column(Modifier.weight(1f).padding(horizontal = 11.dp)) {
            Text(
                server.name,
                color = DarkText,
                fontSize = 15.sp,
                lineHeight = 20.sp,
                fontWeight = FontWeight.Bold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                server.detail,
                color = DarkMuted,
                fontSize = 12.sp,
                lineHeight = 16.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        val shown = shownPing
        Text(
            pingText,
            color = when {
                autoLabel || unavailable || shown == null -> DarkFaint
                else -> phonePingColor(shown)
            },
            fontSize = 13.sp,
            lineHeight = 16.sp,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.padding(end = 8.dp),
        )
        Icon(
            if (selectable) {
                if (active) Icons.Default.RadioButtonChecked else Icons.Default.RadioButtonUnchecked
            } else Icons.Default.ChevronRight,
            null,
            tint = if (active) DarkGreen else DarkGoldLight,
        )
    }
}

/**
 * Country/role badge of a server row: the flag is cropped by the golden ring instead of floating
 * inside it. An emoji flag is a wide rectangle, so at the size it used to be drawn it left a visible
 * gap all around the circle (owner's screen, 04.11.2026). 38 dp matches the prototype flag slot.
 */
@Composable
private fun PhoneServerBadge(flag: String, icon: ImageVector) {
    CountryFlagBadge(flag = flag, icon = icon, size = 38.dp)
}

/**
 * NeuroProtocol entry of the home screen (prototype): gold brain tile, title + «Экспериментально»
 * badge, one-line summary, and a live Вкл/Выкл badge. Always composed — mounting it only when
 * the tunnel becomes available shifted the list mid-connect and read as an overlap (owner video, r706).
 */
@Composable
private fun PhoneNeuroProtocolRow(active: Boolean, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().approvedMobilePanel().heightIn(min = 58.dp)
            .clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        DarkIconTile(Icons.Filled.Psychology)
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "НейроПротокол",
                    color = DarkText,
                    fontSize = 15.sp,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                )
                Spacer(Modifier.width(8.dp))
                DarkBadge("Экспериментально")
            }
            Text(
                "Свой UDP-протокол: нейросеть подстраивает соединение под вашу сеть",
                color = DarkMuted,
                fontSize = 11.sp,
                lineHeight = 14.sp,
            )
        }
        DarkBadge(if (active) "Вкл" else "Выкл", green = active)
        Spacer(Modifier.width(8.dp))
        Icon(Icons.Default.ChevronRight, null, tint = DarkFaint, modifier = Modifier.size(19.dp))
    }
}

/** Full-screen country picker for the folded "JFL" row (phone): every JFL server, live ping. */
@Composable
private fun PhoneJflPicker(
    tags: List<String>,
    selected: String?,
    delays: Map<String, Int>,
    types: Map<String, String>,
    onSelect: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    BackHandler(enabled = true) { onDismiss() }
    Box(Modifier.fillMaxSize().background(DarkBg)) {
        Column(Modifier.fillMaxSize().padding(16.dp)) {
            Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("JFL", color = DarkText, fontSize = 22.sp, fontWeight = FontWeight.Bold)
                    Text(
                        tags.size.toString() + " " + jflServerWord(tags.size),
                        color = DarkGoldLight,
                        fontSize = 12.sp,
                    )
                }
                IconButton(onClick = onDismiss) { Icon(Icons.Default.Close, "Закрыть", tint = DarkGoldLight) }
            }
            Column(Modifier.fillMaxWidth().weight(1f).verticalScroll(rememberScrollState())) {
                tags.forEach { tag ->
                    PhoneServerRow(
                        server = phoneServer(tag, null, types[tag]),
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

/** Latency colours of the approved prototype: ≤300 мс green, 301–600 amber, >600 red. */
private fun phonePingColor(delay: Int): Color = when {
    delay <= 300 -> DarkGreen
    delay <= 600 -> DarkAmber
    else -> DarkRed
}

@Composable
internal fun PhoneBottomNavigation(active: String, home: () -> Unit, servers: () -> Unit, account: () -> Unit, settings: () -> Unit, modifier: Modifier = Modifier) {
    Row(modifier.approvedMobilePanel(navigation = true).padding(horizontal = 20.dp, vertical = 12.dp)) {
        listOf(
            Triple("Главная", Icons.Default.Home, "home"),
            Triple("Серверы", Icons.Default.Public, "servers"),
            Triple("Подписка", Icons.Default.CreditCard, "account"),
            Triple("Настройки", Icons.Default.Tune, "settings"),
        ).zip(listOf(home, servers, account, settings)).forEach { (item, action) ->
            Column(
                Modifier.weight(1f).heightIn(min = 44.dp).clickable(role = Role.Tab, onClick = action),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(3.dp),
            ) {
                val isActive = active == item.third
                Icon(item.second, null, tint = if (isActive) DarkGold else DarkFaint, modifier = Modifier.size(22.dp))
                Text(
                    item.first,
                    color = if (isActive) DarkGoldLight else DarkFaint,
                    fontSize = 10.5.sp,
                    lineHeight = 14.sp,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                )
            }
        }
    }
}

/** Live speed as the prototype shows it: "12,4 Мб/с", "850 КБ/с", "—" when unknown. */
private fun formatSpeed(bytesPerSec: Float?): String {
    if (bytesPerSec == null) return "—"
    val mb = bytesPerSec / (1024f * 1024f)
    return if (mb >= 1f) "%.1f Мб/с".format(Locale.US, mb).replace('.', ',')
    else "${(bytesPerSec / 1024f).roundToInt()} КБ/с"
}

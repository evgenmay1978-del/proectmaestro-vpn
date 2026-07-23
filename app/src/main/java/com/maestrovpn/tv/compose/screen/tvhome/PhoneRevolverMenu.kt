package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.view.accessibility.AccessibilityManager
import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.ScrollableDefaults
import androidx.compose.foundation.gestures.snapping.rememberSnapFlingBehavior
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NetworkCheck
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.component.ChromeTile
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.NeonChip
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlin.math.abs

/**
 * Phone-only cylindrical menu. The fixed frame and eye live outside this composable.
 *
 * Each row tilts away from the viewport centre, producing the vertical movement of a
 * revolver cylinder while keeping the centred row flat and readable. TalkBack receives
 * an ordinary flat list without snap or perspective.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun PhoneRevolverMenu(
    statusText: String,
    connected: Boolean,
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    protocols: List<String>,
    selected: String?,
    hasSubProfile: Boolean,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
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
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val state = rememberLazyListState()
    val accessibilityManager = remember(context) {
        context.getSystemService(AccessibilityManager::class.java)
    }
    var touchExploration by remember(accessibilityManager) {
        mutableStateOf(accessibilityManager?.isTouchExplorationEnabled == true)
    }
    DisposableEffect(accessibilityManager) {
        val listener = AccessibilityManager.TouchExplorationStateChangeListener { enabled ->
            touchExploration = enabled
        }
        accessibilityManager?.addTouchExplorationStateChangeListener(listener)
        onDispose {
            accessibilityManager?.removeTouchExplorationStateChangeListener(listener)
        }
    }
    val snapFling = rememberSnapFlingBehavior(lazyListState = state)
    val flatFling = ScrollableDefaults.flingBehavior()

    val open: (String) -> Unit = remember(context) {
        { url ->
            runCatching {
                context.startActivity(
                    Intent(Intent.ACTION_VIEW, Uri.parse(url))
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            }
        }
    }
    val onUpdate: () -> Unit = {
        (context as? Activity)?.let { activity ->
            scope.launch(Dispatchers.IO) {
                runCatching { Vendor.checkUpdate(activity, true) }
            }
        }
    }
    val onCheckConnection: () -> Unit = {
        scope.launch(Dispatchers.IO) {
            val ok = runCatching {
                (java.net.URL("https://www.google.com/generate_204").openConnection()
                    as java.net.HttpURLConnection).run {
                    connectTimeout = 5_000
                    readTimeout = 5_000
                    try {
                        responseCode in 200..399
                    } finally {
                        disconnect()
                    }
                }
            }.getOrDefault(false)
            withContext(Dispatchers.Main) {
                Toast.makeText(
                    context,
                    if (ok) "Соединение работает" else "Нет соединения",
                    Toast.LENGTH_SHORT,
                ).show()
            }
        }
    }

    val displayProtocols = remember(protocols) { (protocols + "olcrtc").distinct() }
    val protocolRows = remember(displayProtocols) { displayProtocols.chunked(2) }
    val actions = remember(
        onEnterCode,
        onScanQr,
        onSplitTunnel,
        onShareIos,
        onUpdate,
        onCheckConnection,
    ) {
        listOf(
            MenuAction("Ввести логин", Icons.Filled.Search, onEnterCode),
            MenuAction("Сканировать QR", Icons.Filled.QrCode2, onScanQr),
            MenuAction("Приложения через VPN", Icons.Filled.Public, onSplitTunnel),
            MenuAction("Подключить телефон", Icons.Filled.Share, onShareIos),
            MenuAction("Обновить приложение", Icons.Filled.CloudDownload, onUpdate),
            MenuAction("Проверить соединение", Icons.Filled.NetworkCheck, onCheckConnection),
        )
    }
    val actionRows = remember(actions) { actions.chunked(2) }
    val contacts = remember(open) {
        listOf(
            MenuAction("Telegram", Icons.Filled.Send) { open("https://t.me/wapmixx") },
            MenuAction("WhatsApp", Icons.Filled.Chat) { open("https://wa.me/79778116564") },
            MenuAction("МАКС", Icons.Filled.Forum) { open("https://max.ru/") },
        )
    }

    Box(modifier = modifier) {
        LazyColumn(
            state = state,
            flingBehavior = if (touchExploration) flatFling else snapFling,
            contentPadding = PaddingValues(start = 18.dp, top = 8.dp, end = 18.dp, bottom = 28.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.fillMaxSize(),
        ) {
            item(key = "status") {
                RevolverItem("status", state, !touchExploration) {
                    PhoneStatusRow(
                        statusText = statusText,
                        connected = connected,
                        activeProtocol = activeProtocol,
                        selected = selected,
                    )
                }
            }

            if (!accountLogin.isNullOrBlank() || daysLeft != null) {
                item(key = "account") {
                    RevolverItem("account", state, !touchExploration) {
                        AccountCard(
                            login = accountLogin,
                            daysLeft = daysLeft,
                            expires = accountExpires,
                            wood = true,
                            modifier = Modifier
                                .fillMaxWidth()
                                .widthIn(max = 460.dp),
                        )
                    }
                }
            }

            if (protocolRows.isNotEmpty()) {
                item(key = "protocol-title") {
                    RevolverItem("protocol-title", state, !touchExploration) {
                        SectionLabel("ПРОТОКОЛ", wood = true)
                    }
                }
                items(
                    items = protocolRows,
                    key = { row -> "protocol-${row.joinToString("-")}" },
                ) { row ->
                    val key = "protocol-${row.joinToString("-")}"
                    RevolverItem(key, state, !touchExploration) {
                        TwoColumnRow {
                            row.forEach { protocol ->
                                val locked = protocol == "olcrtc" && !hasOlcrtcCreds
                                NeonChip(
                                    label = protocolLabel(protocol),
                                    onClick = {
                                        if (locked) onSelectOlcrtc() else onSelectProtocol(protocol)
                                    },
                                    modifier = Modifier
                                        .weight(1f)
                                        .heightIn(min = 68.dp),
                                    icon = if (locked) Icons.Filled.Lock else protocolIcon(protocol),
                                    selected = protocol == selected && !locked,
                                    subtitle = when {
                                        locked -> "по запросу"
                                        protocol == "olcrtc" -> {
                                            if (olcrtcProvider == "wbstream") "через WB" else "через Яндекс"
                                        }
                                        else -> protocolBadge(protocol)
                                    },
                                    locked = locked,
                                    wood = true,
                                )
                            }
                            if (row.size == 1) Spacer(Modifier.weight(1f))
                        }
                    }
                }
            }

            if (!hasSubProfile) {
                item(key = "trial") {
                    RevolverItem("trial", state, !touchExploration) {
                        GlossyButton(
                            label = "Попробовать 2 дня бесплатно",
                            onClick = onEnterTrial,
                            accent = NeonGreen,
                            icon = Icons.Filled.Bolt,
                            wood = true,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }

            item(key = "buy") {
                RevolverItem("buy", state, !touchExploration) {
                    GlossyButton(
                        label = "Купить подписку",
                        onClick = onBuy,
                        accent = MaestroOrange,
                        icon = Icons.Filled.ShoppingCart,
                        wood = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            items(
                items = actionRows,
                key = { row -> "action-${row.joinToString("-") { it.label }}" },
            ) { row ->
                val key = "action-${row.joinToString("-") { it.label }}"
                RevolverItem(key, state, !touchExploration) {
                    TwoColumnRow {
                        row.forEach { action ->
                            ChromeTile(
                                label = action.label,
                                icon = action.icon,
                                onClick = action.onClick,
                                modifier = Modifier
                                    .weight(1f)
                                    .heightIn(min = 102.dp),
                                wood = true,
                            )
                        }
                        if (row.size == 1) Spacer(Modifier.weight(1f))
                    }
                }
            }

            item(key = "contacts-title") {
                RevolverItem("contacts-title", state, !touchExploration) {
                    SectionLabel("КОНТАКТЫ", wood = true)
                }
            }

            item(key = "phone") {
                RevolverItem("phone", state, !touchExploration) {
                    GlossyButton(
                        label = "8 977 811-65-64",
                        onClick = { open("tel:+79778116564") },
                        accent = NeonGreen,
                        icon = Icons.Filled.Call,
                        wood = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            item(key = "contact-note") {
                RevolverItem("contact-note", state, !touchExploration) {
                    Text(
                        "Если я не ответил на звонок — напишите в любом из мессенджеров.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        textAlign = TextAlign.Center,
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 14.dp, vertical = 4.dp),
                    )
                }
            }

            items(
                items = contacts.chunked(2),
                key = { row -> "contact-${row.joinToString("-") { it.label }}" },
            ) { row ->
                val key = "contact-${row.joinToString("-") { it.label }}"
                RevolverItem(key, state, !touchExploration) {
                    TwoColumnRow {
                        row.forEach { contact ->
                            NeonChip(
                                label = contact.label,
                                onClick = contact.onClick,
                                modifier = Modifier
                                    .weight(1f)
                                    .heightIn(min = 54.dp),
                                icon = contact.icon,
                                iconTint = contactTint(contact.label),
                                wood = true,
                            )
                        }
                        if (row.size == 1) Spacer(Modifier.weight(1f))
                    }
                }
            }
        }

        // Fixed masks hide the outgoing rows behind the carved frame instead of cutting
        // them off on a hard horizontal line.
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(
                    Brush.verticalGradient(
                        0.00f to Color.Black.copy(alpha = 0.76f),
                        0.13f to Color.Transparent,
                        0.84f to Color.Transparent,
                        1.00f to Color.Black.copy(alpha = 0.82f),
                    ),
                ),
        )
    }
}

@Composable
private fun RevolverItem(
    key: String,
    state: LazyListState,
    enabled: Boolean,
    content: @Composable () -> Unit,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .fillMaxWidth()
            .revolverTransform(state, key, enabled),
    ) {
        content()
    }
}

private fun Modifier.revolverTransform(
    state: LazyListState,
    key: String,
    enabled: Boolean,
): Modifier = composed {
    if (!enabled) return@composed this

    val density = LocalDensity.current.density
    graphicsLayer {
        val item = state.layoutInfo.visibleItemsInfo.firstOrNull { it.key == key }
        if (item == null) {
            rotationX = 0f
            scaleX = 1f
            scaleY = 1f
            alpha = 1f
            return@graphicsLayer
        }

        val viewportStart = state.layoutInfo.viewportStartOffset
        val viewportEnd = state.layoutInfo.viewportEndOffset
        val viewportCenter = (viewportStart + viewportEnd) / 2f
        val viewportHalf = ((viewportEnd - viewportStart) / 2f).coerceAtLeast(1f)
        val itemCenter = item.offset + item.size / 2f
        val distance = ((itemCenter - viewportCenter) / viewportHalf).coerceIn(-1f, 1f)
        val edge = abs(distance)

        rotationX = distance * 31f
        scaleX = 1f - edge * 0.10f
        scaleY = 1f - edge * 0.10f
        alpha = 1f - edge * 0.30f
        cameraDistance = density * 18f
        transformOrigin = TransformOrigin.Center
        clip = false
    }
}

@Composable
private fun TwoColumnRow(content: @Composable RowScope.() -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(10.dp),
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth(),
        content = content,
    )
}

private data class MenuAction(
    val label: String,
    val icon: ImageVector,
    val onClick: () -> Unit,
)

private fun contactTint(label: String): Color = when (label) {
    "Telegram" -> Color(0xFF2AABEE)
    "WhatsApp" -> Color(0xFF25D366)
    else -> Color(0xFF2787F5)
}

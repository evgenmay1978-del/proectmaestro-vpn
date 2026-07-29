package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.view.accessibility.AccessibilityManager
import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.ScrollableDefaults
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.snapping.rememberSnapFlingBehavior
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.clickable
import androidx.compose.foundation.selection.selectable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.NetworkCheck
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.premium.MobilePremiumButton
import com.maestrovpn.tv.compose.premium.PremiumEmerald
import com.maestrovpn.tv.compose.premium.PremiumGold
import com.maestrovpn.tv.compose.premium.PremiumGoldMuted
import com.maestrovpn.tv.compose.premium.PremiumLeather
import com.maestrovpn.tv.compose.premium.PremiumText
import com.maestrovpn.tv.compose.premium.PremiumTextMuted
import com.maestrovpn.tv.compose.premium.PremiumTouchTarget
import com.maestrovpn.tv.compose.premium.PremiumWalnut
import com.maestrovpn.tv.utils.ConnectivityCheck
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.collect
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
    val haptic = LocalHapticFeedback.current
    var touchGestureActive by remember { mutableStateOf(false) }
    LaunchedEffect(state, touchExploration, haptic) {
        var previousCenteredKey: Any? = null
        androidx.compose.runtime.snapshotFlow {
            if (touchExploration || !touchGestureActive || !state.isScrollInProgress) {
                null
            } else {
                centeredRevolverKey(state)
            }
        }.collect { centeredKey ->
            when {
                centeredKey == null -> previousCenteredKey = null
                centeredKey != previousCenteredKey -> {
                    if (previousCenteredKey != null) {
                        haptic.performHapticFeedback(HapticFeedbackType.SegmentFrequentTick)
                    }
                    previousCenteredKey = centeredKey
                }
            }
        }
    }

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
            val ok = ConnectivityCheck.isOnline()
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

    Box(modifier = modifier.testTag("premium-revolver")) {
        LazyColumn(
            state = state,
            flingBehavior = if (touchExploration) flatFling else snapFling,
            contentPadding = PaddingValues(start = 18.dp, top = 8.dp, end = 18.dp, bottom = 28.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier
                .fillMaxSize()
                .pointerInput(touchExploration) {
                    awaitEachGesture {
                        awaitFirstDown(requireUnconsumed = false)
                        touchGestureActive = true
                        do {
                            val event = awaitPointerEvent()
                        } while (event.changes.any { it.pressed })
                        touchGestureActive = false
                    }
                },
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
                                .widthIn(max = 460.dp)
                                .testTag("premium-account"),
                        )
                    }
                }
            }

            if (protocolRows.isNotEmpty()) {
                item(key = "protocol-title") {
                    RevolverItem("protocol-title", state, !touchExploration) {
                        PremiumMenuSectionLabel("ПРОТОКОЛ")
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
                                PremiumProtocolTile(
                                    label = protocolLabel(protocol),
                                    onClick = {
                                        if (locked) onSelectOlcrtc() else onSelectProtocol(protocol)
                                    },
                                    modifier = Modifier.weight(1f),
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
                        MobilePremiumButton(
                            label = "Попробовать 2 дня бесплатно",
                            onClick = onEnterTrial,
                            modifier = Modifier.fillMaxWidth(),
                            leadingIcon = { Icon(Icons.Filled.Bolt, null, tint = PremiumEmerald) },
                        )
                    }
                }
            }

            item(key = "buy") {
                RevolverItem("buy", state, !touchExploration) {
                    MobilePremiumButton(
                        label = "Купить подписку",
                        onClick = onBuy,
                        modifier = Modifier.fillMaxWidth(),
                        leadingIcon = { Icon(Icons.Filled.ShoppingCart, null, tint = PremiumGold) },
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
                            PremiumMenuActionTile(
                                label = action.label,
                                icon = action.icon,
                                onClick = action.onClick,
                                modifier = Modifier.weight(1f),
                            )
                        }
                        if (row.size == 1) Spacer(Modifier.weight(1f))
                    }
                }
            }

            item(key = "contacts-title") {
                    RevolverItem("contacts-title", state, !touchExploration) {
                    PremiumMenuSectionLabel("КОНТАКТЫ")
                }
            }

            item(key = "phone") {
                RevolverItem("phone", state, !touchExploration) {
                    MobilePremiumButton(
                        label = "8 977 811-65-64",
                        onClick = { open("tel:+79778116564") },
                        modifier = Modifier.fillMaxWidth(),
                        leadingIcon = { Icon(Icons.Filled.Call, null, tint = PremiumEmerald) },
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
                            PremiumMenuActionTile(
                                label = contact.label,
                                onClick = contact.onClick,
                                modifier = Modifier
                                    .weight(1f),
                                icon = contact.icon,
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
                        0.00f to PremiumWalnut.copy(alpha = 0.88f),
                        0.13f to Color.Transparent,
                        0.84f to Color.Transparent,
                        1.00f to PremiumLeather.copy(alpha = 0.92f),
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
        val visualState = revolverVisualState(distance)
        rotationX = visualState.rotationX
        scaleX = visualState.scale
        scaleY = visualState.scale
        alpha = visualState.alpha
        translationY = visualState.translationY
        cameraDistance = density * 18f
        transformOrigin = TransformOrigin.Center
        clip = false
    }
}

/** A pure, nonlinear drum state. Positive distance is below the viewport centre. */
internal data class RevolverVisualState(
    val rotationX: Float,
    val scale: Float,
    val alpha: Float,
    val translationY: Float,
)

internal fun revolverVisualState(normalizedDistance: Float): RevolverVisualState {
    val distance = normalizedDistance.coerceIn(-1f, 1f)
    val edge = abs(distance)
    // Smoothstep eases the centre flat while tightening the edge of the cylinder.
    val curve = edge * edge * (3f - 2f * edge)
    val direction = if (distance < 0f) -1f else if (distance > 0f) 1f else 0f
    return RevolverVisualState(
        rotationX = direction * curve * 32f,
        scale = 1f - curve * 0.09f,
        alpha = 1f - curve * 0.26f,
        translationY = direction * curve * 14f,
    )
}

private fun centeredRevolverKey(state: LazyListState): Any? {
    val layout = state.layoutInfo
    val viewportCenter = (layout.viewportStartOffset + layout.viewportEndOffset) / 2f
    return layout.visibleItemsInfo.minByOrNull { item ->
        abs(item.offset + item.size / 2f - viewportCenter)
    }?.key
}

@Composable
private fun PremiumMenuSectionLabel(label: String) {
    Text(
        text = label,
        color = PremiumGold,
        fontWeight = FontWeight.Bold,
        style = MaterialTheme.typography.titleMedium,
        textAlign = TextAlign.Center,
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 6.dp),
    )
}

@Composable
internal fun PremiumProtocolTile(
    label: String,
    subtitle: String,
    icon: ImageVector,
    selected: Boolean,
    locked: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .defaultMinSize(minHeight = 76.dp)
            .fantasyFrame(R.drawable.frame_button, selected)
            .background(if (selected) PremiumEmerald.copy(alpha = 0.18f) else PremiumLeather.copy(alpha = 0.62f))
            .alpha(if (locked) 0.72f else 1f)
            .selectable(
                selected = selected,
                role = Role.RadioButton,
                onClick = onClick,
            )
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (selected) PremiumEmerald else PremiumGoldMuted,
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 10.dp),
        ) {
            Text(
                text = label,
                color = if (selected) PremiumText else PremiumTextMuted,
                fontWeight = FontWeight.Bold,
                maxLines = 1,
            )
            Text(
                text = subtitle,
                color = if (selected) PremiumEmerald else PremiumGoldMuted,
                style = MaterialTheme.typography.labelSmall,
                maxLines = 1,
            )
        }
        Icon(
            imageVector = if (selected) Icons.Filled.CheckCircle else Icons.Filled.RadioButtonUnchecked,
            contentDescription = if (selected) "Выбран" else null,
            tint = if (selected) PremiumEmerald else PremiumGoldMuted,
        )
    }
}

@Composable
internal fun PremiumMenuActionTile(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .defaultMinSize(minHeight = PremiumTouchTarget)
            .fantasyFrame(R.drawable.frame_button)
            .background(PremiumLeather.copy(alpha = 0.64f))
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 14.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(7.dp),
    ) {
        Icon(icon, contentDescription = null, tint = PremiumGold)
        Text(
            text = label,
            color = PremiumText,
            style = MaterialTheme.typography.labelLarge,
            textAlign = TextAlign.Center,
            maxLines = 2,
        )
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

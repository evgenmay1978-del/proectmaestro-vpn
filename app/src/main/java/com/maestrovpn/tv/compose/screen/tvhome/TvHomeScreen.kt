package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver

private val ConnGreen = Color(0xFF2FBF71)

/**
 * MaestroVPN home — universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone. Hero is a big ROUND power button in the centre (brutal look): it's
 * the focus target on TV and the obvious tap target on a phone. Plain Material 3
 * (clickable by touch AND D-pad-focusable, unlike tv-material3). The column
 * scrolls so nothing clips; protocol chips wrap (FlowRow); secondary actions are
 * lighter outlined buttons so the connect button stays dominant.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun TvHomeScreen(
    statusText: String,
    connected: Boolean,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String? = null,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit = {},
    onShareIos: () -> Unit = {},
) {
    val isTv = rememberIsTv()
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (isTv) runCatching { connectFocus.requestFocus() }
    }
    val accent = if (connected) ConnGreen else MaestroOrange

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            // Brand wordmark — "Maestro" orange, "VPN" silver.
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "Maestro",
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.Black,
                    color = MaestroOrange,
                )
                Text(
                    "VPN",
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.Black,
                    color = MaestroSilver,
                )
            }
            Spacer(Modifier.height(28.dp))

            // ── Round power button (the hero) ───────────────────────────────
            Box(contentAlignment = Alignment.Center) {
                // brutal outer halo ring
                Box(
                    Modifier
                        .size(216.dp)
                        .border(2.dp, accent.copy(alpha = 0.40f), CircleShape),
                )
                Button(
                    onClick = onToggleConnect,
                    shape = CircleShape,
                    contentPadding = PaddingValues(0.dp),
                    colors = ButtonDefaults.buttonColors(
                        containerColor = accent,
                        contentColor = Color.White,
                    ),
                    elevation = ButtonDefaults.buttonElevation(defaultElevation = 12.dp),
                    modifier = Modifier
                        .size(196.dp)
                        .focusRequester(connectFocus),
                ) {
                    Icon(
                        Icons.Filled.PowerSettingsNew,
                        contentDescription = if (connected) "Отключить" else "Подключить",
                        tint = Color.White,
                        modifier = Modifier.size(96.dp),
                    )
                }
            }

            Spacer(Modifier.height(18.dp))
            // status with a state dot
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(11.dp)
                        .clip(CircleShape)
                        .background(if (connected) ConnGreen else MaestroSilver),
                )
                Spacer(Modifier.width(9.dp))
                Text(
                    statusText.uppercase(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = if (connected) ConnGreen else MaterialTheme.colorScheme.onSurface,
                )
            }

            // Active protocol — the outbound actually carrying traffic right now.
            // With "Авто" the urltest re-picks the lowest-latency protocol live, so
            // this resolves the selector→urltest chain to the real leaf and updates
            // whenever auto switches.
            if (connected && !activeProtocol.isNullOrBlank()) {
                Spacer(Modifier.height(10.dp))
                val viaAuto = selected == "auto" && activeProtocol != "auto"
                Text(
                    if (viaAuto) {
                        "Подключён: ${protocolLabel(activeProtocol)}  •  авто"
                    } else {
                        "Подключён: ${protocolLabel(activeProtocol)}"
                    },
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaestroOrange,
                )
            }

            if (protocols.isNotEmpty()) {
                Spacer(Modifier.height(26.dp))
                Text(
                    "ПРОТОКОЛ",
                    style = MaterialTheme.typography.labelLarge,
                    color = MaestroSilver,
                    letterSpacing = 2.sp,
                )
                Spacer(Modifier.height(10.dp))
                FlowRow(
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    protocols.forEach { protocol ->
                        val label = protocolLabel(protocol)
                        val isSel = protocol == selected
                        if (isSel) {
                            Button(
                                onClick = { onSelectProtocol(protocol) },
                                colors = ButtonDefaults.buttonColors(containerColor = MaestroOrange),
                            ) { Text(label, fontWeight = FontWeight.Bold) }
                        } else {
                            OutlinedButton(onClick = { onSelectProtocol(protocol) }) { Text(label) }
                        }
                    }
                }
            }

            Spacer(Modifier.height(28.dp))
            // primary subscribe action
            Button(
                onClick = onBuy,
                colors = ButtonDefaults.buttonColors(containerColor = MaestroOrange),
                modifier = Modifier.widthIn(min = 260.dp),
            ) { Text("Купить подписку", fontWeight = FontWeight.Bold) }

            // secondary actions — lighter, so the connect button stays the hero
            Spacer(Modifier.height(10.dp))
            OutlinedButton(onClick = onEnterCode, modifier = Modifier.widthIn(min = 260.dp)) {
                Text("Ввести код подписки")
            }
            Spacer(Modifier.height(8.dp))
            OutlinedButton(onClick = onSplitTunnel, modifier = Modifier.widthIn(min = 260.dp)) {
                Text("Приложения через VPN")
            }
            Spacer(Modifier.height(8.dp))
            OutlinedButton(onClick = onShareIos, modifier = Modifier.widthIn(min = 260.dp)) {
                Text("Поделиться подпиской")
            }
        }
    }
}

/** Friendly chip label; "auto" is the urltest pick = the lowest-latency protocol. */
private fun protocolLabel(tag: String): String = when (tag) {
    "auto" -> "Авто (лучший пинг)"
    "hysteria2" -> "Hysteria2"
    "vless" -> "VLESS"
    "naive" -> "NaiveProxy"
    "mieru" -> "Mieru"
    else -> tag.replaceFirstChar { it.uppercase() }
}

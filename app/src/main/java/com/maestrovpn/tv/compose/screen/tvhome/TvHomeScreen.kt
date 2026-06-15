package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding

/**
 * MaestroVPN home — universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone. Uses plain Material 3 (same as the rest of the app's 67 screens),
 * which is clickable by touch AND focusable by D-pad on a TV — unlike tv-material3,
 * which did not react to touch taps on a phone. The column scrolls so nothing is
 * clipped on a short viewport; protocol chips wrap (FlowRow); the launch focus-ring
 * is requested only on TV.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun TvHomeScreen(
    statusText: String,
    connected: Boolean,
    protocols: List<String>,
    selected: String?,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit = {},
) {
    val isTv = rememberIsTv()
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (isTv) runCatching { connectFocus.requestFocus() }
    }

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(text = "MaestroVPN", style = MaterialTheme.typography.headlineLarge)
            Spacer(Modifier.height(8.dp))
            Text(text = statusText, style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.height(32.dp))

            Button(
                onClick = onToggleConnect,
                modifier = Modifier.focusRequester(connectFocus),
            ) {
                Text(if (connected) "Отключить" else "Подключить")
            }

            Spacer(Modifier.height(16.dp))
            Button(onClick = onBuy) {
                Text("Купить подписку")
            }

            if (protocols.isNotEmpty()) {
                Spacer(Modifier.height(24.dp))
                Text(text = "Протокол", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                FlowRow(
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    protocols.forEach { protocol ->
                        val label = protocolLabel(protocol)
                        Button(onClick = { onSelectProtocol(protocol) }) {
                            Text(if (protocol == selected) "● $label" else label)
                        }
                    }
                }
            }

            Spacer(Modifier.height(24.dp))
            Button(onClick = onEnterCode) {
                Text("Ввести код подписки")
            }

            Spacer(Modifier.height(12.dp))
            Button(onClick = onSplitTunnel) {
                Text("Приложения через VPN")
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

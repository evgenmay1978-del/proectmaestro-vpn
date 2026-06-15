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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Button
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding

/**
 * MaestroVPN home — universal connect screen for BOTH a TV remote (D-pad) and a
 * touch phone. tv-material3 Buttons fire on tap and on D-pad center alike; the
 * column scrolls so nothing is clipped on a short phone viewport; the protocol
 * chips wrap (FlowRow) instead of overflowing a narrow screen; the launch
 * focus-ring is requested only on TV (a phone has no focus model to seed).
 */
@OptIn(ExperimentalTvMaterial3Api::class, ExperimentalLayoutApi::class)
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
                        Button(onClick = { onSelectProtocol(protocol) }) {
                            Text(if (protocol == selected) "● $protocol" else protocol)
                        }
                    }
                }
            }

            Spacer(Modifier.height(24.dp))
            Button(onClick = onEnterCode) {
                Text("Ввести код подписки")
            }
        }
    }
}

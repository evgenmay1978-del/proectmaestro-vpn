package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
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

/**
 * MaestroVPN TV home — the D-pad-first connect screen.
 *
 * Pure, stateless UI: the host wires it to DashboardViewModel (status + toggle),
 * the protocol selector group, and the claim-code flow. Kept decoupled from the
 * service/ViewModel so it compiles and previews on its own; the wiring lands in
 * the next increment.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun TvHomeScreen(
    statusText: String,
    connected: Boolean,
    protocols: List<String>,
    selected: String?,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onEnterCode: () -> Unit,
) {
    val connectFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { connectFocus.requestFocus() } }

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(48.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(text = "MaestroVPN TV", style = MaterialTheme.typography.headlineLarge)
            Spacer(Modifier.height(8.dp))
            Text(text = statusText, style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.height(32.dp))

            Button(
                onClick = onToggleConnect,
                modifier = Modifier.focusRequester(connectFocus),
            ) {
                Text(if (connected) "Отключить" else "Подключить")
            }

            if (protocols.isNotEmpty()) {
                Spacer(Modifier.height(24.dp))
                Text(text = "Протокол", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
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

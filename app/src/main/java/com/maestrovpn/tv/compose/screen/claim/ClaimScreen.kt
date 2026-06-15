package com.maestrovpn.tv.compose.screen.claim

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.Button
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding

/**
 * Install-time provisioning screen: the customer types the short code (or their
 * existing panel login) the owner gave them; on success a Remote (auto-updating)
 * profile is created and selected, then [onDone] returns to the home screen.
 *
 * Universal: on a phone, a tap opens the soft keyboard. On a TV the field
 * auto-grabs focus so the D-pad reaches it and the leanback on-screen keyboard
 * opens — otherwise the sibling buttons would steal focus and activation (the
 * only path for existing customers) would be unreachable.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun ClaimScreen(
    onDone: () -> Unit,
    viewModel: ClaimViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsState()
    var code by remember { mutableStateOf("") }
    val busy = state is ClaimState.Busy
    val isTv = rememberIsTv()
    val codeFocus = remember { FocusRequester() }

    LaunchedEffect(state) {
        if (state is ClaimState.Done) onDone()
    }
    LaunchedEffect(Unit) {
        runCatching { codeFocus.requestFocus() }
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
            Text(text = "Активация подписки", style = MaterialTheme.typography.headlineSmall)
            Spacer(Modifier.height(8.dp))
            Text(text = "Введите код или ваш логин", style = MaterialTheme.typography.bodyMedium)
            Spacer(Modifier.height(20.dp))

            OutlinedTextField(
                value = code,
                onValueChange = { code = it },
                singleLine = true,
                enabled = !busy,
                modifier = Modifier
                    .focusRequester(codeFocus)
                    .fillMaxWidth()
                    .widthIn(max = 420.dp),
                label = { androidx.compose.material3.Text("Код или логин") },
            )
            Spacer(Modifier.height(20.dp))

            Button(onClick = { if (code.isNotBlank() && !busy) viewModel.claim(code) }) {
                Text(if (busy) "Проверяем…" else "Активировать")
            }

            (state as? ClaimState.Error)?.let { err ->
                Spacer(Modifier.height(16.dp))
                Text(text = err.message, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

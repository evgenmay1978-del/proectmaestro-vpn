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
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Install-time provisioning screen: the customer types the short code (or their
 * existing panel login) the owner gave them; on success a Remote (auto-updating)
 * profile is created and selected, then [onDone] returns to the home screen.
 *
 * Universal (touch + D-pad), restyled to the "spider" green-glass theme: the field
 * auto-grabs focus so the D-pad reaches it on a TV, and "Активировать" is a glossy
 * green CTA. (A focused field uses the theme's orange = our "selection" accent.)
 */
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
                .drawBehind {
                    val center = Offset(size.width * 0.5f, size.height * 0.30f)
                    val radius = size.maxDimension * 0.5f
                    drawCircle(
                        brush = Brush.radialGradient(
                            listOf(NeonGreen.copy(alpha = 0.08f), Color.Transparent),
                            center = center, radius = radius,
                        ),
                        radius = radius, center = center,
                    )
                }
                .verticalScroll(rememberScrollState())
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "Активация подписки",
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Spacer(Modifier.height(8.dp))
            Text(text = "Введите код или ваш логин", style = MaterialTheme.typography.bodyMedium, color = MaestroSilver)
            Spacer(Modifier.height(22.dp))

            OutlinedTextField(
                value = code,
                onValueChange = { code = it },
                singleLine = true,
                enabled = !busy,
                modifier = Modifier
                    .focusRequester(codeFocus)
                    .fillMaxWidth()
                    .widthIn(max = 420.dp),
                label = { Text("Код или логин") },
            )
            Spacer(Modifier.height(22.dp))

            GlossyButton(
                label = if (busy) "Проверяем…" else "Активировать",
                onClick = { if (code.isNotBlank() && !busy) viewModel.claim(code) },
                accent = NeonGreen,
                modifier = Modifier.widthIn(min = 240.dp),
            )

            (state as? ClaimState.Error)?.let { err ->
                Spacer(Modifier.height(16.dp))
                Text(text = err.message, style = MaterialTheme.typography.bodyMedium, color = Color(0xFFE5484D))
            }
        }
    }
}

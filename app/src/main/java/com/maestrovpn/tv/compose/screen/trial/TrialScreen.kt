package com.maestrovpn.tv.compose.screen.trial

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
 * Free-trial screen for users with NO key: type any nickname → get 2 days. On success an
 * auto-updating Remote profile is created + selected, then [onDone] returns home. Universal
 * (touch + D-pad), "spider" green-glass theme; the field auto-grabs focus for the TV remote.
 */
@Composable
fun TrialScreen(
    onDone: () -> Unit,
    viewModel: TrialViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsState()
    var nick by remember { mutableStateOf("") }
    val busy = state is TrialState.Busy
    val isTv = rememberIsTv()
    val nickFocus = remember { FocusRequester() }

    LaunchedEffect(state) {
        if (state is TrialState.Done) onDone()
    }
    LaunchedEffect(Unit) {
        runCatching { nickFocus.requestFocus() }
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
                text = "Бесплатный пробный период",
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = "Придумайте любой ник и получите 2 дня доступа",
                style = MaterialTheme.typography.bodyMedium,
                color = MaestroSilver,
            )
            Spacer(Modifier.height(22.dp))

            OutlinedTextField(
                value = nick,
                onValueChange = { nick = it },
                singleLine = true,
                enabled = !busy,
                modifier = Modifier
                    .focusRequester(nickFocus)
                    .fillMaxWidth()
                    .widthIn(max = 420.dp),
                label = { Text("Ваш ник") },
            )
            Spacer(Modifier.height(22.dp))

            GlossyButton(
                label = if (busy) "Активируем…" else "Получить 2 дня",
                onClick = { if (nick.isNotBlank() && !busy) viewModel.activate(nick) },
                accent = NeonGreen,
                modifier = Modifier.widthIn(min = 240.dp),
            )

            (state as? TrialState.Error)?.let { err ->
                Spacer(Modifier.height(16.dp))
                Text(text = err.message, style = MaterialTheme.typography.bodyMedium, color = Color(0xFFE5484D))
            }
        }
    }
}

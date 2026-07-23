package com.maestrovpn.tv.compose.screen.trial

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyTextField
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

    // Keep the surface transparent: TV supplies its own scene, while phone draws the shared
    // mobile wood surface below the form.
    Surface(modifier = Modifier.fillMaxSize(), color = Color.Transparent) {
      Box(modifier = Modifier.fillMaxSize()) {
        if (!isTv) {
            Image(
                painter = painterResource(R.drawable.mobile_surface),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                contentScale = ContentScale.Crop,
            )
            Box(Modifier.fillMaxSize().drawBehind { drawRect(Color.Black.copy(alpha = 0.28f)) })
        }
        // Радиал на ТВ убран: banding на 8-битных панелях (фото owner 2026-07-11).
        Box(
            modifier = Modifier
                .fillMaxSize()
                .drawBehind { if (isTv) drawRect(Color(0xFF070909)) }
                .padding(screenPadding(isTv)),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                modifier = Modifier
                    .then(if (isTv) Modifier.widthIn(max = 720.dp) else Modifier.fillMaxWidth())
                    .then(if (isTv) Modifier else Modifier.verticalScroll(rememberScrollState())),
                color = if (isTv) Color(0xFF101313) else Color.Transparent,
                shape = if (isTv) RoundedCornerShape(20.dp) else RoundedCornerShape(0.dp),
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(if (isTv) Color(0xFF101313) else Color.Transparent)
                        .padding(if (isTv) 34.dp else 0.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    Text(
                        text = "Бесплатный пробный период",
                        style = MaterialTheme.typography.headlineSmall,
                        fontFamily = if (isTv) FontFamily.SansSerif else com.maestrovpn.tv.compose.theme.PlayfairFamily,
                        fontWeight = FontWeight.Bold,
                        color = if (isTv) Color(0xFFF3F0E8) else Color(0xFFE8C877),
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = "Придумайте любой ник и получите 2 дня доступа",
                        style = MaterialTheme.typography.bodyMedium,
                        color = if (isTv) Color(0xFFA7AAA3) else MaestroSilver,
                    )
                    Spacer(Modifier.height(if (isTv) 26.dp else 22.dp))

                    FantasyTextField(
                        value = nick,
                        onValueChange = { nick = it },
                        enabled = !busy,
                        singleLine = true,
                        placeholder = "Ваш ник",
                        focusRequester = nickFocus,
                        // widthIn ДО fillMaxWidth — иначе кап мёртв и поле тянулось на весь ТВ-экран
                        modifier = Modifier
                            .widthIn(max = if (isTv) 560.dp else 420.dp)
                            .fillMaxWidth(),
                    )
                    Spacer(Modifier.height(if (isTv) 26.dp else 22.dp))

                    GlossyButton(
                        label = if (busy) "Активируем…" else "Получить 2 дня",
                        onClick = { if (nick.isNotBlank() && !busy) viewModel.activate(nick) },
                        accent = NeonGreen,
                        wood = !isTv,
                        modifier = Modifier.widthIn(min = if (isTv) 280.dp else 240.dp),
                    )

                    (state as? TrialState.Error)?.let { err ->
                        Spacer(Modifier.height(16.dp))
                        Text(text = err.message, style = MaterialTheme.typography.bodyMedium, color = Color(0xFFE5484D))
                    }
                }
            }
        }
      }
    }
}


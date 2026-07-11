package com.maestrovpn.tv.compose.screen.claim

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
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

    // Transparent: на ТВ под экраном лежит глобальное дерево tv_wood_bg (MainActivity) —
    // непрозрачный Surface красил поверх него уныло-тёмную заливку темы (фото owner 2026-07-11).
    // На телефоне фон рисует home_eskiz ниже, так что прозрачность безвредна.
    Surface(modifier = Modifier.fillMaxSize(), color = Color.Transparent) {
      Box(modifier = Modifier.fillMaxSize()) {
        if (!isTv) {
            Image(
                painter = painterResource(R.drawable.home_eskiz),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                contentScale = ContentScale.Crop,
            )
            Box(Modifier.fillMaxSize().drawBehind { drawRect(Color.Black.copy(alpha = 0.45f)) })
        }
        // Радиал на ТВ убран: давал ступенчатый banding на 8-битных панелях (фото owner
        // 2026-07-11); глубина — от виньетки самого фона tv_wood_bg.
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "Активация подписки",
                style = MaterialTheme.typography.headlineSmall,
                fontFamily = com.maestrovpn.tv.compose.theme.PlayfairFamily,
                fontWeight = FontWeight.Bold,
                color = Color(0xFFE8C877),
            )
            Spacer(Modifier.height(8.dp))
            Text(text = "Введите код или ваш логин", style = MaterialTheme.typography.bodyMedium, color = MaestroSilver)
            Spacer(Modifier.height(22.dp))

            FantasyTextField(
                value = code,
                onValueChange = { code = it },
                enabled = !busy,
                singleLine = true,
                placeholder = "Код или логин",
                focusRequester = codeFocus,
                // widthIn ДО fillMaxWidth — иначе кап мёртв и поле тянулось на весь ТВ-экран
                modifier = Modifier
                    .widthIn(max = 420.dp)
                    .fillMaxWidth(),
            )
            Spacer(Modifier.height(22.dp))

            GlossyButton(
                label = if (busy) "Проверяем…" else "Активировать",
                onClick = { if (code.isNotBlank() && !busy) viewModel.claim(code) },
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.widthIn(min = 240.dp),
            )

            (state as? ClaimState.Error)?.let { err ->
                Spacer(Modifier.height(16.dp))
                Text(text = err.message, style = MaterialTheme.typography.bodyMedium, color = Color(0xFFE5484D))
            }
        }
      }
    }
}

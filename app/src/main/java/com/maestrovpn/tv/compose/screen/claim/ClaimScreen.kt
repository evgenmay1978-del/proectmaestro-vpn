package com.maestrovpn.tv.compose.screen.claim

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyTextField
import com.maestrovpn.tv.compose.premium.MobilePremiumButton
import com.maestrovpn.tv.compose.premium.MobilePremiumError
import com.maestrovpn.tv.compose.premium.MobilePremiumPanel
import com.maestrovpn.tv.compose.premium.MobilePremiumScreen
import com.maestrovpn.tv.compose.premium.MobilePremiumTextField
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
    // rememberSaveable: the Activity is recreated on rotation (no configChanges/orientation
    // lock in the manifest), and plain remember dropped the code the user had typed.
    var code by rememberSaveable { mutableStateOf("") }
    val busy = state is ClaimState.Busy
    val isTv = rememberIsTv()
    val codeFocus = remember { FocusRequester() }

    LaunchedEffect(state) {
        if (state is ClaimState.Done) onDone()
    }
    LaunchedEffect(Unit) {
        runCatching { codeFocus.requestFocus() }
    }

    if (!isTv) {
        ClaimPhoneForm(
            code = code,
            onCodeChange = { code = it },
            state = state,
            onClaim = { if (code.isNotBlank() && !busy) viewModel.claim(code) },
            onBack = onDone,
            codeFocus = codeFocus,
        )
    } else {
    // Keep the surface transparent: TV supplies its own graphite scene, while phone draws the
    // shared mobile wood surface below the form.
    Surface(modifier = Modifier.fillMaxSize(), color = Color.Transparent) {
      Box(
          modifier = Modifier
              .fillMaxSize()
              .then(if (isTv) Modifier.background(Color(0xFF070909)) else Modifier),
          contentAlignment = Alignment.Center,
      ) {
        if (!isTv) {
            Image(
                painter = painterResource(R.drawable.mobile_surface),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                contentScale = ContentScale.Crop,
            )
            Box(Modifier.fillMaxSize().drawBehind { drawRect(Color.Black.copy(alpha = 0.28f)) })
        }
        // No radial overlay on TV: it caused visible banding on 8-bit panels.
        Column(
            modifier = Modifier
                .then(
                    if (isTv) {
                        val shape = RoundedCornerShape(24.dp)
                        Modifier
                            .widthIn(max = 720.dp)
                            .fillMaxWidth()
                            .clip(shape)
                            .background(Color(0xFF101313))
                            .border(1.dp, Color(0xFF353A37), shape)
                    } else {
                        Modifier.fillMaxSize()
                    },
                )
                .verticalScroll(rememberScrollState())
                .padding(if (isTv) 36.dp else screenPadding(false)),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "Активация подписки",
                style = MaterialTheme.typography.headlineSmall,
                fontFamily = if (isTv) FontFamily.SansSerif else com.maestrovpn.tv.compose.theme.PlayfairFamily,
                fontWeight = FontWeight.Bold,
                color = if (isTv) Color(0xFFF3F0E8) else Color(0xFFE8C877),
            )
            Spacer(Modifier.height(8.dp))
            Text(text = "Введите код или ваш логин", style = MaterialTheme.typography.bodyMedium, color = if (isTv) Color(0xFFA7AAA3) else MaestroSilver)
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
                    .widthIn(max = if (isTv) 620.dp else 420.dp)
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
}

@Composable
internal fun ClaimPhoneForm(
    code: String,
    onCodeChange: (String) -> Unit,
    state: ClaimState,
    onClaim: () -> Unit,
    onBack: () -> Unit,
    codeFocus: FocusRequester? = null,
) {
    val busy = state is ClaimState.Busy

    MobilePremiumScreen(
        title = "Активация подписки",
        onBack = onBack,
        modifier = Modifier.testTag("premium-claim"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(vertical = 32.dp),
            verticalArrangement = Arrangement.Center,
        ) {
            MobilePremiumPanel {
                MobilePremiumTextField(
                    value = code,
                    onValueChange = onCodeChange,
                    placeholder = "Код или логин",
                    enabled = !busy,
                    focusRequester = codeFocus,
                )
                Spacer(Modifier.height(20.dp))
                MobilePremiumButton(
                    label = if (busy) "Проверяем…" else "Активировать",
                    onClick = onClaim,
                    enabled = code.isNotBlank() && !busy,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            (state as? ClaimState.Error)?.let { err ->
                Spacer(Modifier.height(16.dp))
                MobilePremiumError(
                    message = err.message,
                    onRetry = onClaim,
                )
            }
        }
    }
}


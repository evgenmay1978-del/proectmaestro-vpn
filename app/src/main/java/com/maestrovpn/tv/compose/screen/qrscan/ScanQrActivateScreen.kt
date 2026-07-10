package com.maestrovpn.tv.compose.screen.qrscan

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.screen.claim.ClaimState
import com.maestrovpn.tv.compose.screen.claim.ClaimViewModel
import com.maestrovpn.tv.compose.component.qr.QRScanSheet
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Phone QR-activation: opens the camera scanner and, for a MaestroVPN subscription QR
 * (a /sub URL → [QRScanResult.RemoteProfile]), registers it as the auto-updating profile
 * via [ClaimViewModel] — the same result as typing the code, but by scanning. Pops when
 * done. (TV has no camera; this screen is only reached from the phone-only button.)
 */
@Composable
fun ScanQrActivateScreen(onDone: () -> Unit) {
    val vm: ClaimViewModel = viewModel()
    val state by vm.state.collectAsState()
    var unsupported by remember { mutableStateOf(false) }

    LaunchedEffect(state) {
        if (state is ClaimState.Done) onDone()
    }

    QRScanSheet(
        onDismiss = onDone,
        onScanResult = { result ->
            when (result) {
                is QRScanResult.RemoteProfile -> vm.importSubUrl(result.uri.toString())
                is QRScanResult.QRSData -> unsupported = true
            }
        },
    )

    val errorMessage = (state as? ClaimState.Error)?.message
    if (unsupported || errorMessage != null) {
        val body = errorMessage
            ?: "Это не QR-код подписки MaestroVPN. Отсканируйте QR из бота или с сайта."
        // Dark-Fantasy modal (дерево/золото) — одинаково на телефоне и ТВ
        FantasyDialog(onDismiss = onDone, title = "Не удалось") {
            Text(text = body, color = Color(0xFFECE2CC))
            Spacer(Modifier.height(18.dp))
            GlossyButton(
                label = "OK",
                onClick = onDone,
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

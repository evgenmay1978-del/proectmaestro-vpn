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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.compose.premium.MobilePremiumButton
import com.maestrovpn.tv.compose.premium.MobilePremiumDialogSurface
import com.maestrovpn.tv.compose.premium.PremiumText
import com.maestrovpn.tv.compose.screen.claim.ClaimState
import com.maestrovpn.tv.compose.screen.claim.ClaimViewModel
import com.maestrovpn.tv.compose.component.qr.QRScanSheet

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
        Dialog(onDismissRequest = onDone) {
            MobilePremiumDialogSurface(title = "Не удалось") {
                Text(text = body, color = PremiumText)
                Spacer(Modifier.height(6.dp))
                MobilePremiumButton(
                    label = "OK",
                    onClick = onDone,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

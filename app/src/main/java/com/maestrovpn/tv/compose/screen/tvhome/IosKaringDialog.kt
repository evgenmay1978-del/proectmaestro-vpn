package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.util.QrCodeGenerator
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings

/**
 * "Подписка для iPhone (Karing)" — shows a QR of the customer's UNIVERSAL
 * share-links subscription (`<subUrl>?app=karing`). An iOS user installs Karing
 * (App Store) and scans this to import VLESS + Hysteria2 + Naive. Mieru stays
 * exclusive to this Android app (it needs the bundled local helper).
 */
@Composable
fun IosKaringDialog(onDismiss: () -> Unit) {
    val url by produceState<String?>(null) {
        val pid = Settings.selectedProfile
        value = if (pid != -1L) {
            ProfileManager.get(pid)?.typed?.remoteURL?.takeIf { it.isNotBlank() }?.let {
                val base = it.trimEnd('/')
                base + (if ('?' in base) "&" else "?") + "app=karing"
            }
        } else {
            null
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
        title = { Text("Подписка для iPhone (Karing)") },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                val u = url
                if (u.isNullOrBlank()) {
                    Text(
                        "Сначала активируйте подписку («Купить» или «Ввести код»).",
                        textAlign = TextAlign.Center,
                    )
                } else {
                    Text(
                        "Установите Karing из App Store и отсканируйте этот QR (VLESS + Hysteria2 + Naive):",
                        textAlign = TextAlign.Center,
                    )
                    Spacer(Modifier.height(12.dp))
                    Image(
                        bitmap = QrCodeGenerator.rememberBitmap(u, 640).asImageBitmap(),
                        contentDescription = "QR",
                        modifier = Modifier.size(240.dp),
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(u, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                }
            }
        },
    )
}

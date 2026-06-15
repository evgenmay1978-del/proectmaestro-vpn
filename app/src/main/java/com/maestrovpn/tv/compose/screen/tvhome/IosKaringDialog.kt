package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.util.QRCodeGenerator
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings

/**
 * "Подписка для iPhone (Karing)" — shows a QR of the customer's UNIVERSAL
 * share-links subscription (`<subUrl>?app=karing`). An iOS user installs Karing
 * (App Store) and scans this to import VLESS + Hysteria2 + Naive. Mieru stays
 * exclusive to this Android app (it needs the bundled local helper).
 *
 * The QR is rendered as standard BLACK-on-WHITE (opaque) on a white card with a
 * quiet-zone margin — NOT the theme-aware transparent bitmap. A QR meant to be
 * scanned by another phone's camera must be dark modules on a light background;
 * the app's forced dark theme would otherwise make it inverted (white-on-dark,
 * which iOS/Karing won't decode) or near-invisible (black-on-dark).
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
    // Opaque black-on-white QR, computed off the composition state (keyed on url).
    val qr = remember(url) {
        url?.takeIf { it.isNotBlank() }?.let {
            QRCodeGenerator.generate(it, size = 640, foregroundColor = android.graphics.Color.BLACK, backgroundColor = android.graphics.Color.WHITE)
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
        title = { Text("Подписка для iPhone (Karing)") },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                val u = url
                if (u.isNullOrBlank() || qr == null) {
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
                    // White card + padding = the QR quiet zone, independent of the dark theme.
                    Box(Modifier.background(Color.White).padding(12.dp)) {
                        Image(
                            bitmap = qr.asImageBitmap(),
                            contentDescription = "QR",
                            modifier = Modifier.size(240.dp),
                        )
                    }
                    Spacer(Modifier.height(8.dp))
                    Text(u, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                }
            }
        },
    )
}

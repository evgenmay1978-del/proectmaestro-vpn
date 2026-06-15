package com.maestrovpn.tv.compose.screen.tvhome

import android.graphics.Bitmap
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.util.QRCodeGenerator
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** UI state of the iOS/Karing QR dialog, produced entirely off the main thread. */
private sealed interface IosQr {
    data object Loading : IosQr
    data object NeedActivate : IosQr
    data class Ready(val url: String, val bitmap: Bitmap) : IosQr
    data class Failed(val reason: String) : IosQr
}

/**
 * "Подписка для iPhone (Karing)" — shows a QR of the customer's UNIVERSAL
 * share-links subscription (`<subUrl>?app=karing`). An iOS user installs Karing
 * (App Store) and scans this to import VLESS + Hysteria2 + Naive. Mieru stays
 * exclusive to this Android app (it needs the bundled local helper).
 *
 * ROBUSTNESS: the Room reads (Settings.selectedProfile, ProfileManager.get) AND
 * the ZXing QR encode all run on Dispatchers.IO inside a runCatching, so the
 * produceState coroutine can NEVER throw — an exception there would otherwise
 * crash the whole app. Any failure is surfaced as text in the dialog instead of
 * a crash. The QR is rendered BLACK-on-WHITE (opaque) on a white card with a
 * quiet-zone margin so another phone's camera (iOS/Karing) can actually scan it.
 */
@Composable
fun IosKaringDialog(onDismiss: () -> Unit) {
    val state by produceState<IosQr>(IosQr.Loading) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                val pid = Settings.selectedProfile
                if (pid == -1L) return@runCatching IosQr.NeedActivate
                val remote = ProfileManager.get(pid)?.typed?.remoteURL?.takeIf { it.isNotBlank() }
                    ?: return@runCatching IosQr.NeedActivate
                val base = remote.trimEnd('/')
                val url = base + (if ('?' in base) "&" else "?") + "app=karing"
                val bmp = QRCodeGenerator.generate(
                    content = url, size = 640,
                    foregroundColor = android.graphics.Color.BLACK,
                    backgroundColor = android.graphics.Color.WHITE,
                )
                IosQr.Ready(url, bmp)
            }.getOrElse { e ->
                IosQr.Failed(e.javaClass.simpleName + (e.message?.let { ": $it" } ?: ""))
            }
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
        title = { Text("Подписка для iPhone (Karing)") },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                when (val s = state) {
                    IosQr.Loading -> Text("Загрузка…")
                    IosQr.NeedActivate -> Text(
                        "Сначала активируйте подписку («Купить» или «Ввести код»).",
                        textAlign = TextAlign.Center,
                    )
                    is IosQr.Failed -> Text(
                        "Не удалось создать QR: ${s.reason}",
                        textAlign = TextAlign.Center,
                    )
                    is IosQr.Ready -> {
                        Text(
                            "Установите Karing из App Store и отсканируйте этот QR (VLESS + Hysteria2 + Naive):",
                            textAlign = TextAlign.Center,
                        )
                        Spacer(Modifier.height(12.dp))
                        // White card + padding = the QR quiet zone, independent of the dark theme.
                        Box(Modifier.background(Color.White).padding(12.dp)) {
                            Image(
                                bitmap = s.bitmap.asImageBitmap(),
                                contentDescription = "QR",
                                modifier = Modifier.size(240.dp),
                            )
                        }
                        Spacer(Modifier.height(8.dp))
                        Text(s.url, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                    }
                }
            }
        },
    )
}

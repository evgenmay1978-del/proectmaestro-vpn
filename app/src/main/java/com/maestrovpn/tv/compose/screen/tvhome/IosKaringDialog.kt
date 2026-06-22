package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.util.QRCodeGenerator
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** UI state of the share-subscription dialog, produced off the main thread. */
private sealed interface ShareState {
    data object Loading : ShareState
    data object NeedActivate : ShareState
    data class Ready(val baseUrl: String) : ShareState
    data class Failed(val reason: String) : ShareState
}

/**
 * "Поделиться подпиской" — a QR of the customer's subscription, with an Android /
 * iPhone toggle:
 *   • Android → the FULL sub URL (`/sub/<token>`): all 4 protocols
 *     (VLESS+Hy2+Naive+AnyTLS). Scanned into MaestroVPN it imports as a remote
 *     profile; generic sing-box apps (Hiddify/NekoBox) take the same native set.
 *   • iPhone → `<subUrl>?app=karing`: base64 share-links (VLESS+Hysteria2+Naive) for
 *     Karing.
 *
 * Robustness: all Room reads run off-main in a runCatching (a throw in produceState
 * would crash the app); the QR encode is guarded too. Rendered BLACK-on-WHITE so any
 * camera can scan it regardless of the dark theme.
 */
@Composable
fun IosKaringDialog(onDismiss: () -> Unit) {
    val state by produceState<ShareState>(ShareState.Loading) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                val pid = Settings.selectedProfile
                if (pid == -1L) return@runCatching ShareState.NeedActivate
                val remote = ProfileManager.get(pid)?.typed?.remoteURL?.takeIf { it.isNotBlank() }
                    ?: return@runCatching ShareState.NeedActivate
                ShareState.Ready(remote.trimEnd('/'))
            }.getOrElse { e ->
                ShareState.Failed(e.javaClass.simpleName + (e.message?.let { ": $it" } ?: ""))
            }
        }
    }
    var androidMode by remember { mutableStateOf(true) }

    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
        title = { Text("Поделиться подпиской") },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                when (val s = state) {
                    ShareState.Loading -> Text("Загрузка…")
                    ShareState.NeedActivate -> Text(
                        "Сначала активируйте подписку («Купить» или «Ввести код»).",
                        textAlign = TextAlign.Center,
                    )
                    is ShareState.Failed -> Text(
                        "Не удалось создать QR: ${s.reason}",
                        textAlign = TextAlign.Center,
                    )
                    is ShareState.Ready -> {
                        // Android / iPhone toggle.
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            if (androidMode) {
                                Button(
                                    onClick = { androidMode = true },
                                    colors = ButtonDefaults.buttonColors(containerColor = MaestroOrange),
                                ) { Text("Android") }
                                OutlinedButton(onClick = { androidMode = false }) { Text("iPhone") }
                            } else {
                                OutlinedButton(onClick = { androidMode = true }) { Text("Android") }
                                Button(
                                    onClick = { androidMode = false },
                                    colors = ButtonDefaults.buttonColors(containerColor = MaestroOrange),
                                ) { Text("iPhone") }
                            }
                        }
                        Spacer(Modifier.height(12.dp))

                        val shareUrl = if (androidMode) {
                            s.baseUrl
                        } else {
                            s.baseUrl + (if ('?' in s.baseUrl) "&" else "?") + "app=karing"
                        }
                        Text(
                            if (androidMode) {
                                "Android: отсканируй в MaestroVPN — подключатся все 4 протокола."
                            } else {
                                "iPhone: установи Karing из App Store и отсканируй — VLESS + Hysteria2 + Naive."
                            },
                            textAlign = TextAlign.Center,
                        )
                        Spacer(Modifier.height(12.dp))

                        val qr = remember(shareUrl) {
                            runCatching {
                                QRCodeGenerator.generate(
                                    content = shareUrl, size = 640,
                                    foregroundColor = android.graphics.Color.BLACK,
                                    backgroundColor = android.graphics.Color.WHITE,
                                )
                            }.getOrNull()
                        }
                        if (qr != null) {
                            Box(Modifier.background(Color.White).padding(12.dp)) {
                                Image(
                                    bitmap = qr.asImageBitmap(),
                                    contentDescription = "QR",
                                    modifier = Modifier.size(240.dp),
                                )
                            }
                            Spacer(Modifier.height(8.dp))
                        }
                        Text(shareUrl, style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                    }
                }
            }
        },
    )
}

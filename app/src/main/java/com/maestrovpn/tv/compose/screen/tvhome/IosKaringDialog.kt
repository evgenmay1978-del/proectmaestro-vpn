package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.fantasy.FantasySegmented
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
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
 * Both phone and TV get the Dark-Fantasy modal (carved wood + aged-bronze frame + ivy, an
 * engraved segmented Android/iPhone toggle and a bronze-framed QR).
 * Robustness: all Room reads run off-main in a runCatching (a throw in produceState would
 * crash the app); the QR encode is guarded too and stays BLACK-on-WHITE so any camera scans it.
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

    // ── Dark-Fantasy modal (phone + TV) ──
    FantasyDialog(onDismiss = onDismiss, title = "Поделиться подпиской") {
        ShareBody(state, androidMode, { androidMode = it })
        Spacer(Modifier.height(18.dp))
        GlossyButton(
            label = "Закрыть",
            onClick = onDismiss,
            accent = NeonGreen,
            wood = true,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/** Shared share-dialog body — the toggle + explainer + QR — rendered in the Dark-Fantasy style
 *  (phone + TV). All logic (shareUrl, black-on-white QR) is identical. */
@Composable
private fun ShareBody(
    state: ShareState,
    androidMode: Boolean,
    onMode: (Boolean) -> Unit,
) {
    val bodyColor = MaestroSilver
    Column(
        modifier = Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        when (val s = state) {
            ShareState.Loading -> Text("Загрузка…", color = bodyColor)
            ShareState.NeedActivate -> Text(
                "Сначала активируйте подписку («Купить» или «Ввести код»).",
                textAlign = TextAlign.Center,
                color = bodyColor,
            )
            is ShareState.Failed -> Text(
                "Не удалось создать QR: ${s.reason}",
                textAlign = TextAlign.Center,
                color = bodyColor,
            )
            is ShareState.Ready -> {
                // Android / iPhone toggle.
                FantasySegmented(
                    options = listOf("Android", "iPhone"),
                    selected = if (androidMode) 0 else 1,
                    onSelect = { onMode(it == 0) },
                    modifier = Modifier.fillMaxWidth(),
                )
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
                    color = bodyColor,
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
                    // Bronze QR frame — a SQUARE frame scaled uniformly (corners stay proportional,
                    // no 9-patch thinning) around a WHITE quiet-zone; the code itself stays
                    // black-on-white & fully scannable. Frame fills the dialog width (matches the
                    // эскиз quar.png); the white card fills the frame opening, leaving only a thin
                    // wood reveal. Responsive so it holds on any width. QR stays BLACK-on-WHITE.
                    Box(
                        Modifier
                            .fillMaxWidth(0.84f)
                            .aspectRatio(1f),
                        contentAlignment = Alignment.Center,
                    ) {
                        Image(
                            painter = painterResource(R.drawable.frame_qr),
                            contentDescription = null,
                            modifier = Modifier.matchParentSize(),
                            contentScale = ContentScale.FillBounds,
                        )
                        Box(
                            modifier = Modifier
                                .fillMaxSize(0.80f)
                                .clip(RoundedCornerShape(16.dp))
                                .background(Color.White),
                            contentAlignment = Alignment.Center,
                        ) {
                            Image(
                                bitmap = qr.asImageBitmap(),
                                contentDescription = "QR",
                                modifier = Modifier.fillMaxSize(0.86f),
                            )
                        }
                    }
                    Spacer(Modifier.height(8.dp))
                }
                Text(
                    shareUrl,
                    style = MaterialTheme.typography.bodySmall,
                    textAlign = TextAlign.Center,
                    color = GoldMid,
                )
            }
        }
    }
}

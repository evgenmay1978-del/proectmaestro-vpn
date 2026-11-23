package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
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
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.fantasy.FantasySegmented
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.util.QRCodeGenerator
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.MaestroSub
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** UI state of the share-subscription dialog, produced off the main thread. */
internal sealed interface ShareState {
    data object Loading : ShareState
    data object NeedActivate : ShareState
    data class Ready(val baseUrl: String) : ShareState
    data class Failed(val reason: String) : ShareState
}

/** Loads the subscription URL off the main thread; shared by the dialog and the phone device page. */
@Composable
internal fun rememberShareState(): ShareState {
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
    return state
}

/**
 * Subscription URL for one of the apps on the device page / share dialog
 * (MaestroVPN 0, Happ 1, Incy 2, Karing 3): the base URL without device metadata,
 * with the format the app needs (xray links for Happ/Incy, plain links for Karing).
 */
internal fun shareUrlForApp(baseUrl: String, appIndex: Int): String {
    val source = android.net.Uri.parse(MaestroSub.stripDeviceMetadata(baseUrl))
    val builder = source.buildUpon().clearQuery()
    source.queryParameterNames.filterNot { it == "format" || it == "app" }.forEach { name ->
        source.getQueryParameters(name).forEach { builder.appendQueryParameter(name, it) }
    }
    when (appIndex) {
        1, 2 -> builder.appendQueryParameter("format", "xray")
        3 -> builder.appendQueryParameter("format", "links")
    }
    return builder.build().toString()
}

/** Display form of a subscription URL: scheme + host + first path segment, token masked. */
internal fun maskSubUrl(url: String): String {
    val segments = url.split("/")
    return if (segments.size > 4) segments.take(4).joinToString("/") + "/••••••••" else url
}

/**
 * "Поделиться подпиской" — a QR of the customer's subscription, with an Android /
 * iPhone toggle:
 *   • Android → the FULL sub URL (`/sub/<token>`): all subscription protocols
 *     (VLESS+Hysteria2). Scanned into MaestroVPN it imports as a remote
 *     profile; generic sing-box apps (Hiddify/NekoBox) take the same native set.
 *   • iPhone → `<subUrl>?app=karing`: base64 share-links (VLESS+Hysteria2) for
 *     Karing.
 *
 * Both phone and TV get the Dark-Fantasy modal (carved wood + aged-bronze frame + ivy, an
 * engraved segmented Android/iPhone toggle and a bronze-framed QR).
 * TV-only since 23.11.2026: the phone share path moved to the device page
 * (PhoneDashboard "device"); this dialog is no longer reachable on phone.
 * Robustness: all Room reads run off-main in a runCatching (a throw in produceState would
 * crash the app); the QR encode is guarded too and stays BLACK-on-WHITE so any camera scans it.
 */
@Composable
fun IosKaringDialog(onDismiss: () -> Unit) {
    val state = rememberShareState()
    // TV-only: the home action is explicitly labelled "Подключить iPhone", so open on the
    // iPhone/Karing QR. The segmented control still switches either way.
    var androidMode by remember { mutableStateOf(false) }

    // TV keeps its established D-pad dialog, iPhone-first default and compact QR geometry.
    FantasyDialog(onDismiss = onDismiss, title = "Поделиться подпиской") {
        ShareBody(state, androidMode, { androidMode = it })
        Spacer(Modifier.height(18.dp))
        GlossyButton(
            label = "Закрыть",
            onClick = onDismiss,
            accent = NeonGreen,
            wood = true,
            modifier = Modifier.fillMaxWidth(0.42f),
        )
    }
}

/** Shared share-dialog body — the toggle + explainer + QR — rendered in the Dark-Fantasy style
 *  (TV-only since 23.11.2026). All logic (shareUrl, black-on-white QR) is identical. */
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
                        "Android: отсканируй в MaestroVPN — подключатся все протоколы подписки."
                    } else {
                        "iPhone: установи Karing из App Store и отсканируй — VLESS + Hysteria2."
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
                // Служебную строку-URL под QR больше не печатаем: три строки сырой ссылки
                // выглядели тех-мусором (фото owner 2026-07-11). Сам QR несёт ту же ссылку.
                if (qr != null) {
                    // QR «в раме картины» (урок qr-decorative-frame-lesson): белая quiet-zone
                    // карточка рисуется ПЕРВОЙ (снизу), безель с ПРОЗРАЧНЫМ проёмом — ПОВЕРХ,
                    // карточка чуть больше проёма и заправлена под бронзу → рамка цельная,
                    // никакого «белый квадрат поверх рамки» (фото owner 2026-07-12).
                    // ТВ: белая карточка 210 dp с QR внутри.
                    Box(
                        modifier = Modifier.size(210.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .clip(RoundedCornerShape(16.dp))
                                .background(Color.White),
                            contentAlignment = Alignment.Center,
                        ) {
                            Image(
                                bitmap = qr.asImageBitmap(),
                                contentDescription = "QR",
                                modifier = Modifier.fillMaxSize(0.88f),
                            )
                        }
                    }
                    Spacer(Modifier.height(8.dp))
                }
            }
        }
    }
}

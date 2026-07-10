package com.maestrovpn.tv.compose.component.qr

import android.content.Intent
import android.graphics.Color
import android.net.Uri
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.qrs.QRSConstants
import com.maestrovpn.tv.qrs.QRSEncoder
import kotlinx.coroutines.delay

@Composable
fun QRSDialog(profileData: ByteArray, profileName: String, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()
    var fps by remember { mutableIntStateOf(QRSConstants.DEFAULT_FPS) }
    var sliceSize by remember { mutableIntStateOf(QRSConstants.DEFAULT_SLICE_SIZE) }

    val encoder = remember(sliceSize) { QRSEncoder(sliceSize) }
    val dataWithMeta = remember(profileData, profileName) {
        QRSEncoder.appendFileHeaderMeta(
            data = profileData,
            filename = "$profileName.bpf",
            contentType = "application/octet-stream",
        )
    }
    val requiredFrames = remember(dataWithMeta, sliceSize) {
        QRSConstants.calculateRequiredFrames(dataWithMeta.size, sliceSize)
    }
    val frames = remember(dataWithMeta, sliceSize, requiredFrames) {
        encoder.encode(dataWithMeta, QRSConstants.OFFICIAL_URL_PREFIX)
            .take(requiredFrames)
            .toList()
    }

    val frameInterval = remember(fps) { 1000L / fps }

    val generator = remember(frames) {
        QRSBitmapGenerator(
            scope = coroutineScope,
            frames = frames,
            foregroundColor = Color.BLACK,
            backgroundColor = Color.WHITE,
            bufferSize = QRSConstants.BITMAP_BUFFER_SIZE,
        )
    }

    val generationState by generator.state.collectAsState()

    LaunchedEffect(generator) {
        generator.start()
    }

    DisposableEffect(generator) {
        onDispose {
            generator.cancel()
        }
    }

    LaunchedEffect(frameInterval, generationState.generatedCount) {
        if (generationState.generatedCount > 0) {
            while (true) {
                delay(frameInterval)
                generator.advanceFrame()
            }
        }
    }

    val openQrsInfo: () -> Unit = {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/qifi-dev/qrs"))
        context.startActivity(intent)
    }

    // ── Dark-Fantasy modal (phone + TV) ──
    FantasyDialog(onDismiss = onDismiss, title = "QR") {
        // Bronze-framed white QR card (black-on-white, fully scannable) — same recipe as
        // the share dialog: a square bronze frame around a white rounded quiet-zone card.
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
                    .background(androidx.compose.ui.graphics.Color.White),
                contentAlignment = Alignment.Center,
            ) {
                generationState.currentBitmap?.let { bitmap ->
                    Image(
                        bitmap = bitmap.asImageBitmap(),
                        contentDescription = stringResource(R.string.content_description_qr_code),
                        modifier = Modifier.fillMaxSize(0.90f),
                        contentScale = ContentScale.Fit,
                    )
                }
            }
        }

        Spacer(Modifier.height(18.dp))

        // FPS control.
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = stringResource(R.string.qrs_fps),
                color = MaestroSilver,
                fontWeight = FontWeight.Medium,
            )
            Text(
                text = "$fps Hz",
                style = MaterialTheme.typography.bodySmall,
                color = GoldMid,
            )
        }
        Slider(
            value = fps.toFloat(),
            onValueChange = { fps = it.toInt() },
            valueRange = QRSConstants.MIN_FPS.toFloat()..QRSConstants.MAX_FPS.toFloat(),
            colors = fantasySliderColors(),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(8.dp))

        // Slice-size control.
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = stringResource(R.string.qrs_slice_size),
                color = MaestroSilver,
                fontWeight = FontWeight.Medium,
            )
            Text(
                text = "$sliceSize",
                style = MaterialTheme.typography.bodySmall,
                color = GoldMid,
            )
        }
        Slider(
            value = sliceSize.toFloat(),
            onValueChange = { sliceSize = it.toInt() },
            valueRange = QRSConstants.MIN_SLICE_SIZE.toFloat()..QRSConstants.MAX_SLICE_SIZE.toFloat(),
            colors = fantasySliderColors(),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(18.dp))

        GlossyButton(
            label = stringResource(R.string.close),
            onClick = onDismiss,
            accent = NeonGreen,
            wood = true,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(6.dp))
        TextButton(onClick = openQrsInfo) {
            Text(stringResource(R.string.qrs_what_is_qrs), color = GoldMid, fontWeight = FontWeight.Medium)
        }
    }
}

/** Bronze/emerald tint for the Material [Slider] on the phone Dark-Fantasy path. */
@Composable
private fun fantasySliderColors() = SliderDefaults.colors(
    thumbColor = GoldHi,
    activeTrackColor = NeonGreen,
    inactiveTrackColor = GoldMid.copy(alpha = 0.35f),
)

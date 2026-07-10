package com.maestrovpn.tv.compose.component.qr

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Показ готового QR-битмапа во весь диалог.
 *
 * Dark-Fantasy modal (phone + TV): резное дерево + бронзовая рамка frame_qr вокруг белой
 *   скруглённой карточки; QR остаётся ЧЁРНО-БЕЛЫМ, чтобы любая камера его считала.
 *
 * Логика идентична: тот же [bitmap], тот же [onDismiss]; меняется только визуальный слой.
 */
@Composable
fun QRCodeDialog(bitmap: Bitmap, onDismiss: () -> Unit) {
    // ── Dark-Fantasy modal (phone + TV) ──
    FantasyDialog(onDismiss = onDismiss, title = "QR-код") {
        // Bronze QR frame — квадратная рамка frame_qr вокруг БЕЛОЙ скруглённой карточки,
        // которая заполняет проём (тонкий деревянный ободок по краю). QR внутри остаётся
        // ЧЁРНО-БЕЛЫМ и полностью считываемым.
        Box(
            Modifier
                .fillMaxWidth(0.92f)
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
                    bitmap = bitmap.asImageBitmap(),
                    contentDescription = stringResource(com.maestrovpn.tv.R.string.content_description_qr_code),
                    modifier = Modifier.fillMaxSize(0.86f),
                )
            }
        }
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

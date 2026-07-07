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
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Показ готового QR-битмапа во весь диалог.
 *
 * PHONE → Dark-Fantasy modal (резное дерево + бронзовая рамка frame_qr вокруг белой карточки,
 *   как в IosKaringDialog): QR остаётся ЧЁРНО-БЕЛЫМ на белой скруглённой карточке, чтобы любая
 *   камера его считала.
 * TV → прежний Material [Dialog]+[Card] (не трогаем — живой флот на 1ГБ).
 *
 * Логика идентична: тот же [bitmap], тот же [onDismiss]; меняется только визуальный слой.
 */
@Composable
fun QRCodeDialog(bitmap: Bitmap, onDismiss: () -> Unit) {
    val isTv = rememberIsTv()
    if (isTv) {
        // ── TV: original Material dialog (unchanged) ──
        Dialog(
            onDismissRequest = onDismiss,
            properties = DialogProperties(usePlatformDefaultWidth = false),
        ) {
            Card(
                modifier =
                Modifier
                    .fillMaxWidth(0.9f)
                    .wrapContentHeight(),
                shape = RoundedCornerShape(16.dp),
                colors =
                CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                ),
            ) {
                Surface(
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(1f),
                    shape = RoundedCornerShape(0.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant,
                ) {
                    Box(
                        modifier =
                        Modifier
                            .fillMaxSize()
                            .background(MaterialTheme.colorScheme.surface),
                        contentAlignment = Alignment.Center,
                    ) {
                        Image(
                            bitmap = bitmap.asImageBitmap(),
                            contentDescription = stringResource(com.maestrovpn.tv.R.string.content_description_qr_code),
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
    } else {
        // ── PHONE: Dark-Fantasy modal ──
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
}

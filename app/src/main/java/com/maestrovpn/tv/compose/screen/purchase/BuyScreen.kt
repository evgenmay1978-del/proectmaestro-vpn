package com.maestrovpn.tv.compose.screen.purchase

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.layout.heightIn
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.NeonChip
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.util.QRCodeGenerator

/**
 * In-app purchase screen (works on touch + D-pad): pick a tariff → see СБП payment
 * details → the box polls and auto-activates once the owner confirms the payment.
 * Restyled to the "spider" green-glass theme: tariffs are glossy orange CTAs, "Я
 * оплатил" is a glossy green confirm; the payment QR stays black-on-white so any
 * camera can scan it.
 */
@Composable
fun BuyScreen(
    onDone: () -> Unit,
    viewModel: BuyViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsState()
    val isTv = rememberIsTv()

    LaunchedEffect(state) {
        if (state is BuyState.Done) onDone()
    }

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .drawBehind {
                    val center = Offset(size.width * 0.5f, size.height * 0.22f)
                    val radius = size.maxDimension * 0.5f
                    drawCircle(
                        brush = Brush.radialGradient(
                            listOf(NeonGreen.copy(alpha = 0.07f), Color.Transparent),
                            center = center, radius = radius,
                        ),
                        radius = radius, center = center,
                    )
                }
                .verticalScroll(rememberScrollState())
                .padding(screenPadding(isTv)),
            horizontalAlignment = Alignment.CenterHorizontally,
            // Top (not Center): with verticalScroll, Arrangement.Center pushes the top of
            // tall content (the "Сумма: X ₽" line on the payment screen) ABOVE the viewport
            // and out of scroll reach — the customer couldn't see how much to pay. Top keeps
            // the amount visible at the top and the rest reachable by scrolling.
            verticalArrangement = Arrangement.Top,
        ) {
            when (val s = state) {
                is BuyState.Loading -> Text("Загрузка тарифов…", style = MaterialTheme.typography.titleMedium)

                is BuyState.Tariffs -> {
                    Text(
                        "Выберите подписку",
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = Color.White,
                    )
                    Spacer(Modifier.height(18.dp))
                    // compact green-glass tariff chips (app style); the focused one is clearly
                    // highlighted (brighter border + scale + glow) so a D-pad user sees the pick.
                    // On a TV the first tariff grabs focus so the remote starts on a visible item.
                    val firstFocus = remember { FocusRequester() }
                    LaunchedEffect(Unit) { if (isTv) runCatching { firstFocus.requestFocus() } }
                    s.items.forEachIndexed { i, t ->
                        NeonChip(
                            label = "${t.name}  —  ${t.rub} ₽",
                            onClick = { viewModel.buy(t.key) },
                            modifier = Modifier
                                .fillMaxWidth()
                                .widthIn(max = 360.dp)
                                .heightIn(min = 50.dp)
                                .padding(vertical = 4.dp)
                                .then(if (i == 0) Modifier.focusRequester(firstFocus) else Modifier),
                            icon = Icons.Filled.Star,
                        )
                    }
                }

                is BuyState.AwaitingPayment -> {
                    // On TV the viewport is SHORT (landscape). The "Оплата" header is redundant
                    // (you reached this screen by buying) and its height was part of what pushed
                    // the amount off the top: the focusable "Я оплатил" button auto-scrolls into
                    // view, dragging the top of a too-tall column above the viewport. Drop it on
                    // TV so the amount line is the top. Phone (tall, no focus-scroll) keeps it.
                    if (!isTv) {
                        Text(
                            "Оплата",
                            style = MaterialTheme.typography.headlineSmall,
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                        )
                        Spacer(Modifier.height(8.dp))
                    }
                    Text(
                        "Сумма: ${s.rub} ₽",
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = NeonGreen,
                        textAlign = TextAlign.Center,
                    )
                    // Scan-to-pay QR. Prefer the cross-bank pay link (T-Bank «Сбор денег» / СБП)
                    // so the phone opens a real payment page — pay from any bank, no acquiring.
                    // Falls back to a QR of the СБП phone number if no pay link is configured.
                    val payContent = if (s.payUrl.isNotBlank()) s.payUrl else s.phone
                    if (payContent.isNotBlank()) {
                        val payQr = remember(payContent) { QRCodeGenerator.generate(payContent) }
                        Spacer(Modifier.height(if (isTv) 10.dp else 16.dp))
                        Surface(color = Color.White) {
                            Image(
                                bitmap = payQr.asImageBitmap(),
                                contentDescription = "QR для оплаты",
                                modifier = Modifier
                                    .padding(12.dp)
                                    // Smaller on TV so the whole payment card fits the short
                                    // landscape viewport WITHOUT scrolling (scrolling hid the
                                    // amount). 170dp ≈ 340px on 1080p — still easily phone-scannable.
                                    .size(if (isTv) 170.dp else 220.dp),
                            )
                        }
                        Spacer(Modifier.height(8.dp))
                        Text(
                            if (s.payUrl.isNotBlank()) {
                                "Отсканируйте телефоном — откроется оплата (СБП или картой, из любого банка)"
                            } else {
                                "Отсканируйте телефоном — номер вводить не нужно"
                            },
                            style = MaterialTheme.typography.bodyMedium,
                            textAlign = TextAlign.Center,
                        )
                    }
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Код заказа (укажите в сообщении к переводу, если есть поле):",
                        style = MaterialTheme.typography.bodyLarge,
                        textAlign = TextAlign.Center,
                    )
                    Text(s.code, style = MaterialTheme.typography.headlineMedium, color = NeonGreen)
                    if (s.phone.isNotBlank()) {
                        Spacer(Modifier.height(12.dp))
                        Text(
                            "Или вручную по СБП на номер: ${s.phone}",
                            style = MaterialTheme.typography.bodyMedium,
                            textAlign = TextAlign.Center,
                        )
                    }
                    Spacer(Modifier.height(if (isTv) 16.dp else 24.dp))
                    GlossyButton(
                        label = "Я оплатил",
                        onClick = { viewModel.iPaid() },
                        accent = NeonGreen,
                        modifier = Modifier.widthIn(min = 220.dp),
                    )
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "После нажатия заявка уйдёт владельцу. Подписка активируется после подтверждения — оставьте экран открытым.",
                        style = MaterialTheme.typography.bodyMedium,
                        textAlign = TextAlign.Center,
                    )
                }

                is BuyState.AwaitingConfirm -> Text(
                    "Ожидаем подтверждение оплаты…",
                    style = MaterialTheme.typography.titleMedium,
                    textAlign = TextAlign.Center,
                )

                is BuyState.Activating -> Text("Активируем подписку…", style = MaterialTheme.typography.titleMedium)

                is BuyState.Done -> Text("Готово!", style = MaterialTheme.typography.titleMedium, color = NeonGreen)

                is BuyState.Error -> {
                    Text("Ошибка: ${s.message}", style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
                    Spacer(Modifier.height(20.dp))
                    GlossyButton(label = "Повторить", onClick = { viewModel.loadTariffs() }, accent = NeonGreen)
                }
            }
        }
    }
}

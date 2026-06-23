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
import androidx.compose.material3.Button
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.screenPadding
import com.maestrovpn.tv.compose.util.QRCodeGenerator

/**
 * In-app purchase screen (works on touch + D-pad): pick a tariff → see СБП payment
 * details → the box polls and auto-activates once the owner confirms the payment.
 * Plain Material 3 so taps register on a phone and the D-pad focuses on a TV.
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
                    Text("Выберите подписку", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(24.dp))
                    s.items.forEach { t ->
                        Button(
                            onClick = { viewModel.buy(t.key) },
                            modifier = Modifier
                                .fillMaxWidth()
                                .widthIn(max = 420.dp)
                                .padding(vertical = 6.dp),
                        ) {
                            Text("${t.name}   —   ${t.rub} ₽")
                        }
                    }
                }

                is BuyState.AwaitingPayment -> {
                    Text("Оплата", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "Сумма: ${s.rub} ₽",
                        style = MaterialTheme.typography.headlineSmall,
                        textAlign = TextAlign.Center,
                    )
                    // Scan-to-pay QR. Prefer the cross-bank pay link (T-Bank «Сбор денег» / СБП)
                    // so the phone opens a real payment page — pay from any bank, no acquiring.
                    // Falls back to a QR of the СБП phone number if no pay link is configured.
                    val payContent = if (s.payUrl.isNotBlank()) s.payUrl else s.phone
                    if (payContent.isNotBlank()) {
                        val payQr = remember(payContent) { QRCodeGenerator.generate(payContent) }
                        Spacer(Modifier.height(16.dp))
                        Surface(color = Color.White) {
                            Image(
                                bitmap = payQr.asImageBitmap(),
                                contentDescription = "QR для оплаты",
                                modifier = Modifier
                                    .padding(12.dp)
                                    .size(220.dp),
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
                    Text(s.code, style = MaterialTheme.typography.headlineMedium)
                    if (s.phone.isNotBlank()) {
                        Spacer(Modifier.height(12.dp))
                        Text(
                            "Или вручную по СБП на номер: ${s.phone}",
                            style = MaterialTheme.typography.bodyMedium,
                            textAlign = TextAlign.Center,
                        )
                    }
                    Spacer(Modifier.height(24.dp))
                    Button(onClick = { viewModel.iPaid() }) {
                        Text("Я оплатил")
                    }
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

                is BuyState.Done -> Text("Готово!", style = MaterialTheme.typography.titleMedium)

                is BuyState.Error -> {
                    Text("Ошибка: ${s.message}", style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
                    Spacer(Modifier.height(20.dp))
                    Button(onClick = { viewModel.loadTariffs() }) { Text("Повторить") }
                }
            }
        }
    }
}

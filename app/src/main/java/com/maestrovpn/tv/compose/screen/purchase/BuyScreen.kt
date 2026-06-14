package com.maestrovpn.tv.compose.screen.purchase

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.Button
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text

/**
 * In-app purchase screen (all D-pad, no typing): pick a tariff → see СБП payment
 * details → the box polls and auto-activates once the owner confirms the payment.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun BuyScreen(
    onDone: () -> Unit,
    viewModel: BuyViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsState()

    LaunchedEffect(state) {
        if (state is BuyState.Done) onDone()
    }

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(48.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
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
                                .width(420.dp)
                                .padding(vertical = 6.dp),
                        ) {
                            Text("${t.name}   —   ${t.rub} ₽")
                        }
                    }
                }

                is BuyState.AwaitingPayment -> {
                    Text("Оплата по СБП", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(16.dp))
                    Text(
                        "Переведите ${s.rub} ₽ по СБП на номер:",
                        style = MaterialTheme.typography.bodyLarge,
                        textAlign = TextAlign.Center,
                    )
                    Text(s.phone, style = MaterialTheme.typography.headlineMedium)
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "В комментарии к переводу укажите код:",
                        style = MaterialTheme.typography.bodyLarge,
                        textAlign = TextAlign.Center,
                    )
                    Text(s.code, style = MaterialTheme.typography.headlineMedium)
                    Spacer(Modifier.height(24.dp))
                    Text(
                        "После подтверждения оплаты подписка активируется автоматически — оставьте этот экран открытым.",
                        style = MaterialTheme.typography.bodyMedium,
                        textAlign = TextAlign.Center,
                    )
                }

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

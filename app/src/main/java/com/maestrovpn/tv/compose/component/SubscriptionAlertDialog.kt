package com.maestrovpn.tv.compose.component

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import com.maestrovpn.tv.utils.SubscriptionHealth

/**
 * One modal for the two authoritative panel answers about this subscription: 402 (expired)
 * and 404/410 (the token is gone). Network failures and 5xx never open it — see
 * [SubscriptionHealth] and the silent updater.
 *
 * Before this the app silently kept a dead profile and the only visible error was a raw
 * transport message, so a deleted subscription looked like "CDN is down" (21.10.2026).
 */
@Composable
fun SubscriptionAlertDialog(
    alert: SubscriptionHealth.Alert,
    onPrimary: () -> Unit,
    onDismiss: () -> Unit,
) {
    val (title, body, primary) = when (alert.kind) {
        SubscriptionHealth.Kind.EXPIRED -> Triple(
            "Подписка истекла",
            "Срок действия закончился. Продлите подписку, чтобы снова подключиться.",
            "Продлить",
        )
        SubscriptionHealth.Kind.REVOKED -> Triple(
            "Подписка не найдена",
            "Сервер больше не знает этот ключ. Активируйте заново по логину или коду.",
            "Активировать",
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = { TextButton(onClick = onPrimary) { Text(primary) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Позже") } },
    )
}

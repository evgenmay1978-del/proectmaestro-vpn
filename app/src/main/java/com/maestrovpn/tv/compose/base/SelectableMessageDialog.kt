package com.maestrovpn.tv.compose.base

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen

@Composable
fun SelectableMessageDialog(title: String, message: String, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val scrollState = rememberScrollState()
    val copy: () -> Unit = {
        clipboard.setText(AnnotatedString(message))
        Toast.makeText(context, context.getString(R.string.copied_to_clipboard), Toast.LENGTH_SHORT).show()
    }

    if (rememberIsTv()) {
        AlertDialog(
            onDismissRequest = onDismiss,
            title = { Text(title) },
            text = {
                Box(Modifier.heightIn(max = 320.dp).verticalScroll(scrollState)) {
                    SelectionContainer { Text(message) }
                }
            },
            dismissButton = {
                TextButton(onClick = copy) { Text(stringResource(R.string.per_app_proxy_action_copy)) }
            },
            confirmButton = {
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.ok)) }
            },
        )
    } else {
        FantasyDialog(onDismiss = onDismiss, title = title) {
            Box(Modifier.fillMaxWidth().heightIn(max = 320.dp).verticalScroll(scrollState)) {
                SelectionContainer { Text(message, color = MaestroSilver) }
            }
            Spacer(Modifier.height(18.dp))
            GlossyButton(
                label = stringResource(R.string.ok),
                onClick = onDismiss,
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(4.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) {
                TextButton(onClick = copy) {
                    Text(stringResource(R.string.per_app_proxy_action_copy), color = GoldMid, fontWeight = FontWeight.Medium)
                }
            }
        }
    }
}

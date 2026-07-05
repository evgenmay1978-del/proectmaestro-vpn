package com.maestrovpn.tv.compose.component

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.jeziellago.compose.markdowntext.MarkdownText
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.FantasyDialog
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.update.UpdateInfo
import org.kodein.emoji.Emoji
import org.kodein.emoji.EmojiTemplateCatalog
import org.kodein.emoji.all

@Composable
fun UpdateAvailableDialog(updateInfo: UpdateInfo, onDismiss: () -> Unit, onUpdate: () -> Unit) {
    val context = LocalContext.current
    val emojiCatalog = remember { EmojiTemplateCatalog(Emoji.all()) }
    val processedNotes = remember(updateInfo.releaseNotes) {
        updateInfo.releaseNotes?.takeIf { it.isNotBlank() }?.let { emojiCatalog.replaceShortcodes(it) }
    }
    val viewRelease: () -> Unit = {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(updateInfo.releaseUrl)))
        onDismiss()
    }

    if (rememberIsTv()) {
        // ── TV: Material dialog (unchanged) ──
        AlertDialog(
            onDismissRequest = onDismiss,
            title = { Text(stringResource(R.string.check_update)) },
            text = {
                Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                    Text(
                        text = stringResource(R.string.new_version_available, updateInfo.versionName),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    if (processedNotes != null) {
                        Spacer(Modifier.height(12.dp))
                        MarkdownText(
                            markdown = processedNotes,
                            style = MaterialTheme.typography.bodySmall.copy(
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            ),
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = { onDismiss(); onUpdate() }) { Text(stringResource(R.string.update)) }
            },
            dismissButton = {
                Row {
                    TextButton(onClick = viewRelease) { Text(stringResource(R.string.view_release)) }
                    Spacer(Modifier.width(8.dp))
                    TextButton(onClick = onDismiss) { Text(stringResource(android.R.string.cancel)) }
                }
            },
        )
    } else {
        // ── PHONE: Dark-Fantasy modal ──
        FantasyDialog(onDismiss = onDismiss, title = stringResource(R.string.check_update)) {
            Text(
                text = stringResource(R.string.new_version_available, updateInfo.versionName),
                style = MaterialTheme.typography.bodyMedium,
                color = Color(0xFFECE2CC),
            )
            if (processedNotes != null) {
                Spacer(Modifier.height(12.dp))
                Box(Modifier.heightIn(max = 260.dp).verticalScroll(rememberScrollState())) {
                    MarkdownText(
                        markdown = processedNotes,
                        style = MaterialTheme.typography.bodySmall.copy(color = MaestroSilver),
                    )
                }
            }
            Spacer(Modifier.height(18.dp))
            GlossyButton(
                label = stringResource(R.string.update),
                onClick = { onDismiss(); onUpdate() },
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(6.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceEvenly,
            ) {
                TextButton(onClick = viewRelease) {
                    Text(stringResource(R.string.view_release), color = GoldMid, fontWeight = FontWeight.Medium)
                }
                TextButton(onClick = onDismiss) {
                    Text(stringResource(android.R.string.cancel), color = GoldMid.copy(alpha = 0.7f))
                }
            }
        }
    }
}

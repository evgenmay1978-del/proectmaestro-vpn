package com.maestrovpn.tv.compose.fantasy

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.GoldHi

/**
 * Dark-Fantasy modal shell — replaces the flat grey Material3 [androidx.compose.material3.AlertDialog].
 * A carved dark-wood panel with an aged-bronze frame + ivy corners (sliced from the эскиз), an engraved
 * serif-gold title and a thin gold rule under it. Callers drop their content (toggles, QR, buttons)
 * into [content]; the ornate frame + decoration come for free.
 */
@Composable
fun FantasyDialog(
    onDismiss: () -> Unit,
    title: String,
    modifier: Modifier = Modifier,
    dismissOnClickOutside: Boolean = true,
    content: @Composable ColumnScope.() -> Unit,
) {
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(
            usePlatformDefaultWidth = false,
            dismissOnClickOutside = dismissOnClickOutside,
        ),
    ) {
        // widthIn BEFORE fillMaxWidth — the old order made the 460dp cap dead (fillMaxWidth's
        // fixed constraints can't be shrunk by a later widthIn), so a landscape dialog blew up to
        // 0.92×window and its square QR box pushed the buttons off-screen. heightIn + the
        // scrollable body below keep every action reachable on short windows.
        val dialogMaxH = (LocalConfiguration.current.screenHeightDp * 0.92f).dp
        Column(
            modifier
                .widthIn(max = 460.dp)
                .fillMaxWidth(0.92f)
                .heightIn(max = dialogMaxH)
                .fantasyFrame(R.drawable.frame_panel)
                .padding(horizontal = 22.dp, vertical = 20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = title,
                color = GoldHi,
                fontFamily = PlayfairFamily,
                fontWeight = FontWeight.Bold,
                fontSize = 22.sp,
                letterSpacing = 1.sp,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(4.dp))
            // engraved diamond ornament (вензель) under the title
            Text("◆", color = GoldHi, fontSize = 14.sp)
            Spacer(Modifier.height(14.dp))
            Column(
                Modifier.weight(1f, fill = false).verticalScroll(rememberScrollState()),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                content()
            }
        }
    }
}

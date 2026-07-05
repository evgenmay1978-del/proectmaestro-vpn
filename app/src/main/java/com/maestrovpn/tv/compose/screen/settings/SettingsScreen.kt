package com.maestrovpn.tv.compose.screen.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.background
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Favorite
import androidx.compose.material.icons.outlined.FilterAlt
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.SettingsRemote
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.update.UpdateState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(navController: NavController) {
    OverrideTopBar {
        TopAppBar(
            title = {
                Text(
                    stringResource(R.string.title_settings),
                    fontFamily = PlayfairFamily,
                    fontWeight = FontWeight.Bold,
                    color = Color(0xFFE8C877),
                    letterSpacing = 1.sp,
                )
            },
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = Color(0xFF17110A),
                titleContentColor = Color(0xFFE8C877),
            ),
        )
    }

    val context = LocalContext.current
    val hasUpdate by UpdateState.hasUpdate

    val openUrl: (String) -> Unit = { url ->
        val intent = android.content.Intent(android.content.Intent.ACTION_VIEW)
        intent.data = android.net.Uri.parse(url)
        context.startActivity(intent)
    }
    val openInNew: @Composable () -> Unit = {
        Icon(Icons.AutoMirrored.Outlined.OpenInNew, contentDescription = null, tint = GoldMid, modifier = Modifier.size(20.dp))
    }

    FantasyScreenBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            // ── Настройки ──
            FantasyListRow(
                title = stringResource(R.string.title_app_settings),
                icon = Icons.Outlined.Info,
                onClick = { navController.navigate("settings/app") },
                trailing = if (hasUpdate) {
                    { Box(Modifier.size(10.dp).clip(CircleShape).background(NeonGreen)) }
                } else {
                    null
                },
            )
            FantasyListRow(
                title = stringResource(R.string.core),
                icon = Icons.Outlined.Settings,
                onClick = { navController.navigate("settings/core") },
            )
            FantasyListRow(
                title = stringResource(R.string.service),
                icon = Icons.Outlined.Tune,
                onClick = { navController.navigate("settings/service") },
            )
            FantasyListRow(
                title = stringResource(R.string.profile_override),
                icon = Icons.Outlined.FilterAlt,
                onClick = { navController.navigate("settings/profile_override") },
            )
            FantasyListRow(
                title = stringResource(R.string.remote_control),
                icon = Icons.Outlined.SettingsRemote,
                onClick = { navController.navigate("settings/remote_control") },
            )

            Spacer(Modifier.height(8.dp))
            SectionLabel(stringResource(R.string.about).uppercase(), wood = true)

            FantasyListRow(
                title = stringResource(R.string.error_deprecated_documentation),
                icon = Icons.Outlined.Description,
                onClick = { openUrl("https://sing-box.sagernet.org/") },
                trailing = openInNew,
            )
            FantasyListRow(
                title = stringResource(R.string.source_code),
                icon = Icons.Outlined.Code,
                onClick = { openUrl("https://github.com/SagerNet/sing-box-for-android") },
                trailing = openInNew,
            )
            FantasyListRow(
                title = stringResource(R.string.sponsor),
                icon = Icons.Outlined.Favorite,
                onClick = { openUrl("https://sekai.icu/sponsors/") },
                trailing = openInNew,
            )
            Spacer(Modifier.height(16.dp))
        }
    }
}

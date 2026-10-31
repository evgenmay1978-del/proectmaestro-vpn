package com.maestrovpn.tv.compose.screen.settings

import com.maestrovpn.tv.compose.premium.PhoneScreenHeader
import com.maestrovpn.tv.compose.premium.PremiumPhoneContentInset
import android.content.Intent
import android.net.VpnService
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.VpnKey
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.aivpn.client.AivpnJni
import com.maestrovpn.tv.R
import com.maestrovpn.tv.aivpn.AivpnKey
import com.maestrovpn.tv.aivpn.AivpnStatus
import com.maestrovpn.tv.aivpn.AivpnTunnel
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.fantasy.FantasyTextField
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.ktx.clipboardText
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/**
 * Screen for the experimental AIVPN protocol (opened from the main-screen tile): paste an
 * aivpn:// key, connect, disconnect.
 *
 * The screen only stores the key and forwards start/stop to [AivpnTunnel]; it never touches the
 * native library directly, so it stays usable (with a clear error) on builds that ship without
 * libaivpn_core.so.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AivpnScreen(navController: NavController) {
    val phoneArtwork = !com.maestrovpn.tv.compose.rememberIsTv()
    OverrideTopBar {
        if (phoneArtwork) {
            PhoneScreenHeader(
                title = stringResource(R.string.aivpn_settings),
                onBack = { navController.navigateUp() },
                modifier = Modifier.padding(horizontal = PremiumPhoneContentInset),
                backContentDescription = stringResource(R.string.content_description_back),
            )
        } else {
            TopAppBar(
                title = {
                    Text(
                        stringResource(R.string.aivpn_settings),
                        fontFamily = PlayfairFamily,
                        fontWeight = FontWeight.Bold,
                        color = Color(0xFFE8C877),
                        letterSpacing = 1.sp,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.content_description_back),
                            tint = GoldHi,
                        )
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Color(0xFF17110A),
                    titleContentColor = Color(0xFFE8C877),
                ),
            )
        }
    }

    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var keyText by remember { mutableStateOf(Settings.aivpnKey) }
    var error by remember { mutableStateOf<String?>(null) }
    val status by AivpnTunnel.status.collectAsState()
    val active = status is AivpnStatus.Connecting || status is AivpnStatus.Running

    // Bytes in an aivpn:// key carry no whitespace; pasted keys often arrive line-wrapped.
    val cleanedKey = remember(keyText) { keyText.filterNot { it.isWhitespace() } }
    val parsed = remember(cleanedKey) { runCatching { AivpnKey.parse(cleanedKey) } }
    val parsedKey = parsed.getOrNull()

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) {
        // The system dialog is finished either way; the service re-checks VpnService.prepare()
        // and reports a refusal as a normal failure instead of dying.
        AivpnTunnel.start(context)
    }

    val connect: () -> Unit = {
        val key = parsedKey
        if (cleanedKey.isEmpty()) {
            error = context.getString(R.string.aivpn_error_no_key)
        } else if (!AivpnJni.isAvailable) {
            error = context.getString(R.string.aivpn_error_library_missing, AivpnJni.loadError.orEmpty())
        } else if (key == null) {
            error = context.getString(
                R.string.aivpn_error_bad_key,
                parsed.exceptionOrNull()?.message.orEmpty(),
            )
        } else {
            error = null
            keyText = cleanedKey
            scope.launch(Dispatchers.IO) { Settings.aivpnKey = cleanedKey }
            val request: Intent? = VpnService.prepare(context)
            if (request != null) {
                permissionLauncher.launch(request)
            } else {
                AivpnTunnel.start(context)
            }
        }
    }

    val statusText = when (val current = status) {
        is AivpnStatus.Failed -> stringResource(R.string.aivpn_status_failed, current.message)
        AivpnStatus.Running -> stringResource(R.string.aivpn_status_running)
        AivpnStatus.Connecting -> stringResource(R.string.aivpn_status_connecting)
        AivpnStatus.Stopped -> stringResource(R.string.aivpn_status_stopped)
    }

    FantasyScreenBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = PremiumPhoneContentInset, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            FantasyListRow(
                title = stringResource(R.string.aivpn_status_title),
                subtitle = statusText,
                icon = Icons.Outlined.Info,
            )

            SectionLabel(stringResource(R.string.aivpn_settings).uppercase(), wood = true)

            FantasyTextField(
                value = keyText,
                onValueChange = {
                    keyText = it
                    error = null
                },
                placeholder = stringResource(R.string.aivpn_key_hint),
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )

            if (parsedKey != null) {
                FantasyListRow(
                    title = stringResource(R.string.aivpn_server_title),
                    subtitle = parsedKey.serverAddress,
                    icon = Icons.Outlined.VpnKey,
                )
            }

            GlossyButton(
                label = stringResource(R.string.aivpn_paste),
                onClick = {
                    val text = clipboardText
                    if (text.isNullOrBlank()) {
                        error = context.getString(R.string.aivpn_clipboard_empty)
                    } else {
                        keyText = text.filterNot { it.isWhitespace() }
                        error = null
                    }
                },
                accent = GoldHi,
                icon = Icons.Outlined.ContentPaste,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )

            GlossyButton(
                label = if (active) {
                    stringResource(R.string.aivpn_disconnect)
                } else {
                    stringResource(R.string.aivpn_connect)
                },
                onClick = {
                    if (active) {
                        AivpnTunnel.stop(context)
                    } else {
                        connect()
                    }
                },
                accent = NeonGreen,
                modifier = Modifier.fillMaxWidth(),
            )

            error?.let {
                Text(it, color = Color(0xFFE5484D), fontSize = 13.sp)
            }

            if (!AivpnJni.isAvailable) {
                Text(
                    stringResource(R.string.aivpn_error_library_missing, AivpnJni.loadError.orEmpty()),
                    color = Color(0xFFE8C877),
                    fontSize = 13.sp,
                )
            }

            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.aivpn_hint),
                color = GoldHi.copy(alpha = 0.8f),
                fontSize = 13.sp,
            )
            Spacer(Modifier.height(16.dp))
        }
    }
}

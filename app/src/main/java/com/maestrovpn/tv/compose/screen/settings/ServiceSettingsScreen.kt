package com.maestrovpn.tv.compose.screen.settings

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.ClickableText
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.BatteryChargingFull
import androidx.compose.material.icons.outlined.VpnKey
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.maestrovpn.tv.R
import com.maestrovpn.tv.bg.ServiceConnection
import com.maestrovpn.tv.compose.base.UiEvent
import com.maestrovpn.tv.compose.base.rememberApplyServiceChangeNotifier
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.fantasy.FantasyToggle
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.constant.Status
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.ktx.launchCustomTab
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServiceSettingsScreen(
    navController: NavController,
    serviceConnection: ServiceConnection? = null,
    serviceStatus: Status = Status.Stopped,
) {
    OverrideTopBar {
        TopAppBar(
            title = {
                Text(
                    stringResource(R.string.service),
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
                        tint = Color(0xFFE8C877),
                    )
                }
            },
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = Color(0xFF17110A),
                titleContentColor = Color(0xFFE8C877),
            ),
        )
    }

    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var isBatteryOptimizationIgnored by remember { mutableStateOf(false) }
    var allowBypass by remember { mutableStateOf(Settings.allowBypass) }
    val notifyApplyChange = rememberApplyServiceChangeNotifier(serviceStatus)
    val requestBatteryOptimizationLauncher =
        rememberLauncherForActivityResult(
            ActivityResultContracts.StartActivityForResult(),
        ) { _ ->
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                val pm = context.getSystemService(PowerManager::class.java)
                isBatteryOptimizationIgnored =
                    pm?.isIgnoringBatteryOptimizations(context.packageName) == true
            }
        }

    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val pm = context.getSystemService(PowerManager::class.java)
            isBatteryOptimizationIgnored =
                pm?.isIgnoringBatteryOptimizations(context.packageName) == true
        } else {
            isBatteryOptimizationIgnored = true
        }
    }

    // Shared logic callbacks (identical for TV + phone) ──────────────────────────
    val requestBatteryOptimization: () -> Unit = {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val intent =
                Intent(
                    android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:${context.packageName}"),
                )
            requestBatteryOptimizationLauncher.launch(intent)
        }
    }
    val onAllowBypassChange: (Boolean) -> Unit = { checked ->
        allowBypass = checked
        scope.launch(Dispatchers.IO) {
            Settings.allowBypass = checked
            withContext(Dispatchers.Main) {
                notifyApplyChange(UiEvent.ApplyServiceChange.Mode.Reload)
            }
        }
    }

    // ── Dark-Fantasy kit ─────────────────────────────────────────────────────
    FantasyScreenBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (!isBatteryOptimizationIgnored && Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                // Carved-wood warning panel (aged-bronze frame) with the two actions.
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .fantasyFrame(R.drawable.frame_panel)
                        .padding(horizontal = 18.dp, vertical = 16.dp),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(
                            imageVector = Icons.Outlined.BatteryChargingFull,
                            contentDescription = null,
                            tint = NeonGreen,
                            modifier = Modifier.padding(end = 12.dp),
                        )
                        Text(
                            stringResource(R.string.background_permission),
                            fontFamily = PlayfairFamily,
                            fontWeight = FontWeight.Bold,
                            fontSize = 20.sp,
                            color = Color(0xFFE8C877),
                        )
                    }

                    Text(
                        stringResource(R.string.background_permission_description),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaestroSilver,
                        modifier = Modifier.padding(top = 10.dp, bottom = 16.dp),
                    )

                    GlossyButton(
                        label = stringResource(R.string.request_background_permission),
                        onClick = requestBatteryOptimization,
                        accent = NeonGreen,
                        wood = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Spacer(Modifier.height(6.dp))
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.Center,
                    ) {
                        androidx.compose.material3.TextButton(
                            onClick = { context.launchCustomTab("https://dontkillmyapp.com/") },
                        ) {
                            Text(
                                stringResource(R.string.read_more),
                                color = GoldMid,
                                fontWeight = FontWeight.Medium,
                            )
                        }
                    }
                }
            }

            // ── VPN section ──
            SectionLabel("VPN", wood = true)

            FantasyListRow(
                title = stringResource(R.string.allow_bypass),
                icon = Icons.Outlined.VpnKey,
                trailing = {
                    FantasyToggle(
                        checked = allowBypass,
                        onCheckedChange = onAllowBypassChange,
                    )
                },
            )

            // allow_bypass description + a tappable documentation link (bronze card).
            val descriptionText = stringResource(R.string.allow_bypass_description)
            val linkText = stringResource(R.string.android_documentation)
            val annotatedString = buildAnnotatedString {
                withStyle(SpanStyle(color = MaestroSilver)) {
                    append(descriptionText)
                }
                append("\n\n")
                pushStringAnnotation(tag = "URL", annotation = ALLOW_BYPASS_DOC_URL)
                withStyle(
                    SpanStyle(
                        color = GoldMid,
                        textDecoration = TextDecoration.Underline,
                    ),
                ) {
                    append(linkText)
                }
                pop()
            }
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .fantasyFrame(R.drawable.frame_bar)
                    .padding(horizontal = 18.dp, vertical = 14.dp),
            ) {
                ClickableText(
                    text = annotatedString,
                    style = MaterialTheme.typography.bodyMedium,
                    onClick = { offset ->
                        annotatedString.getStringAnnotations(
                            tag = "URL",
                            start = offset,
                            end = offset,
                        ).firstOrNull()?.let {
                            context.launchCustomTab(it.item)
                        }
                    },
                )
            }

            Spacer(Modifier.height(16.dp))
        }
    }
}

private const val ALLOW_BYPASS_DOC_URL =
    "https://developer.android.com/reference/android/net/VpnService.Builder#allowBypass()"

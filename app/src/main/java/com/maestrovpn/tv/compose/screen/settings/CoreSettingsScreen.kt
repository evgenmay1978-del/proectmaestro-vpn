package com.maestrovpn.tv.compose.screen.settings

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.provider.DocumentsContract
import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.outlined.DeleteForever
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.fantasy.FantasyToggle
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.ktx.clipboardText
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun CoreSettingsScreen(navController: NavController) {
    OverrideTopBar {
        TopAppBar(
            title = {
                Text(
                    stringResource(R.string.core),
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
    var dataSize by remember { mutableStateOf("") }
    val version = remember { Libbox.version() }
    var showVersionMenu by remember { mutableStateOf(false) }
    var disableDeprecatedWarnings by remember { mutableStateOf(Settings.disableDeprecatedWarnings) }

    // Calculate data size on launch
    LaunchedEffect(Unit) {
        withContext(Dispatchers.IO) {
            val filesDir = context.getExternalFilesDir(null) ?: context.filesDir
            val size =
                filesDir.walkTopDown()
                    .filter { it.isFile }
                    .map { it.length() }
                    .sum()
            val formattedSize = Libbox.formatBytes(size)
            dataSize = formattedSize
        }
    }

    // Shared logic callbacks (identical for TV + phone) ────────────────────────
    val copyVersion: () -> Unit = {
        clipboardText = version
        Toast.makeText(
            context,
            R.string.copied_to_clipboard,
            Toast.LENGTH_SHORT,
        ).show()
        showVersionMenu = false
    }
    val onDestroy: () -> Unit = {
        scope.launch(Dispatchers.IO) {
            val filesDir = context.getExternalFilesDir(null) ?: context.filesDir
            filesDir.deleteRecursively()
            filesDir.mkdirs()

            // Recalculate data size
            val newSize =
                filesDir.walkTopDown()
                    .filter { it.isFile }
                    .map { it.length() }
                    .sum()
            val formattedSize = Libbox.formatBytes(newSize)
            dataSize = formattedSize
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
            // Version info — long-press to copy
            Box {
                FantasyListRow(
                    title = stringResource(R.string.core_version_title),
                    subtitle = version,
                    icon = Icons.Outlined.Info,
                    modifier = Modifier.combinedClickable(
                        onClick = {},
                        onLongClick = { showVersionMenu = true },
                    ),
                )
                Box(modifier = Modifier.align(Alignment.BottomEnd)) {
                    DropdownMenu(
                        expanded = showVersionMenu,
                        onDismissRequest = { showVersionMenu = false },
                    ) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.per_app_proxy_action_copy)) },
                            leadingIcon = {
                                Icon(
                                    imageVector = Icons.Filled.ContentCopy,
                                    contentDescription = null,
                                )
                            },
                            onClick = copyVersion,
                        )
                    }
                }
            }

            // Data size
            FantasyListRow(
                title = stringResource(R.string.core_data_size),
                subtitle = dataSize.ifEmpty { stringResource(R.string.calculating) },
                icon = Icons.Outlined.Storage,
            )

            if (version.contains("-")) {
                Spacer(Modifier.height(6.dp))
                SectionLabel(stringResource(R.string.beta_settings).uppercase(), wood = true)
                FantasyListRow(
                    title = stringResource(R.string.disable_deprecated_warnings),
                    icon = Icons.Outlined.WarningAmber,
                    trailing = {
                        FantasyToggle(
                            checked = disableDeprecatedWarnings,
                            onCheckedChange = { checked ->
                                disableDeprecatedWarnings = checked
                                scope.launch(Dispatchers.IO) {
                                    Settings.disableDeprecatedWarnings = checked
                                }
                            },
                        )
                    },
                )
            }

            // Working directory section
            Spacer(Modifier.height(6.dp))
            SectionLabel(stringResource(R.string.working_directory).uppercase(), wood = true)

            FantasyListRow(
                title = stringResource(R.string.browse),
                icon = Icons.Outlined.FolderOpen,
                onClick = { openInFileManager(context) },
            )
            FantasyListRow(
                title = stringResource(R.string.destroy),
                icon = Icons.Outlined.DeleteForever,
                iconTint = MaterialTheme.colorScheme.error,
                onClick = onDestroy,
            )
            Spacer(Modifier.height(16.dp))
        }
    }
}

private fun openInFileManager(context: Context) {
    val authority = "${context.packageName}.workingdir"
    val rootUri = DocumentsContract.buildRootUri(authority, "working_directory")

    val intent = Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(rootUri, DocumentsContract.Document.MIME_TYPE_DIR)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
    }

    try {
        context.startActivity(intent)
    } catch (e: ActivityNotFoundException) {
        Toast.makeText(
            context,
            context.getString(R.string.no_file_manager),
            Toast.LENGTH_SHORT,
        ).show()
    }
}

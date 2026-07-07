package com.maestrovpn.tv.compose.screen.settings

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.SettingsRemote
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.database.RemoteServer
import com.maestrovpn.tv.database.RemoteServerManager
import com.maestrovpn.tv.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun RemoteControlScreen(navController: NavController) {
    val isTv = rememberIsTv()

    OverrideTopBar {
        if (isTv) {
            TopAppBar(
                title = { Text(stringResource(R.string.remote_control)) },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.content_description_back),
                        )
                    }
                },
                actions = {
                    IconButton(onClick = { navController.navigate("settings/remote_control/new") }) {
                        Icon(
                            imageVector = Icons.Outlined.Add,
                            contentDescription = stringResource(R.string.remote_new_server),
                        )
                    }
                },
            )
        } else {
            TopAppBar(
                title = {
                    Text(
                        stringResource(R.string.remote_control),
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
                            tint = GoldMid,
                        )
                    }
                },
                actions = {
                    IconButton(onClick = { navController.navigate("settings/remote_control/new") }) {
                        Icon(
                            imageVector = Icons.Outlined.Add,
                            contentDescription = stringResource(R.string.remote_new_server),
                            tint = GoldMid,
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

    val scope = rememberCoroutineScope()
    var servers by remember { mutableStateOf<List<RemoteServer>>(emptyList()) }
    val activeRemoteServer by RemoteControlManager.remoteServer.collectAsState()

    LaunchedEffect(Unit) {
        servers = withContext(Dispatchers.IO) { RemoteServerManager.list() }
    }
    DisposableEffect(Unit) {
        val callback: () -> Unit = {
            scope.launch {
                servers = withContext(Dispatchers.IO) { RemoteServerManager.list() }
            }
        }
        RemoteServerManager.registerCallback(callback)
        onDispose {
            RemoteServerManager.unregisterCallback(callback)
        }
    }

    // Shared delete callback — identical logic for both UIs.
    val onDelete: (RemoteServer) -> Unit = { server ->
        scope.launch(Dispatchers.IO) {
            if (RemoteControlManager.remoteServer.value?.id == server.id) {
                withContext(Dispatchers.Main) {
                    RemoteControlManager.exitRemoteControl()
                }
            }
            RemoteServerManager.delete(server)
        }
    }

    if (!isTv) {
        // ─────────────── Dark-Fantasy (phone) ───────────────
        FantasyScreenBackground {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp, vertical = 14.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                SectionLabel(stringResource(R.string.remote_servers).uppercase(), wood = true)

                if (servers.isEmpty()) {
                    FantasyListRow(
                        title = stringResource(R.string.remote_no_servers),
                        icon = Icons.Outlined.SettingsRemote,
                    )
                } else {
                    servers.forEach { server ->
                        var showMenu by remember { mutableStateOf(false) }
                        Box {
                            FantasyListRow(
                                title = server.displayName,
                                subtitle = if (server.name.isNotEmpty()) server.url else null,
                                icon = Icons.Outlined.SettingsRemote,
                                modifier = Modifier.combinedClickable(
                                    onClick = {
                                        navController.navigate(
                                            "settings/remote_control/edit/${server.id}",
                                        )
                                    },
                                    onLongClick = { showMenu = true },
                                ),
                                trailing = if (activeRemoteServer?.id == server.id) {
                                    {
                                        Icon(
                                            imageVector = Icons.Default.CheckCircle,
                                            contentDescription = null,
                                            tint = NeonGreen,
                                            modifier = Modifier.size(22.dp),
                                        )
                                    }
                                } else {
                                    null
                                },
                            )
                            DropdownMenu(
                                expanded = showMenu,
                                onDismissRequest = { showMenu = false },
                            ) {
                                DropdownMenuItem(
                                    text = { Text(stringResource(R.string.edit)) },
                                    leadingIcon = {
                                        Icon(Icons.Outlined.Edit, contentDescription = null)
                                    },
                                    onClick = {
                                        showMenu = false
                                        navController.navigate(
                                            "settings/remote_control/edit/${server.id}",
                                        )
                                    },
                                )
                                DropdownMenuItem(
                                    text = {
                                        Text(
                                            stringResource(R.string.menu_delete),
                                            color = MaterialTheme.colorScheme.error,
                                        )
                                    },
                                    leadingIcon = {
                                        Icon(
                                            Icons.Outlined.Delete,
                                            contentDescription = null,
                                            tint = MaterialTheme.colorScheme.error,
                                        )
                                    },
                                    onClick = {
                                        showMenu = false
                                        onDelete(server)
                                    },
                                )
                            }
                        }
                    }
                }
                Spacer(Modifier.height(16.dp))
            }
        }
        return
    }

    // ─────────────── Material (TV — unchanged) ───────────────
    Column(
        modifier =
        Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surface)
            .verticalScroll(rememberScrollState())
            .padding(vertical = 8.dp),
    ) {
        Text(
            text = stringResource(R.string.remote_servers),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(horizontal = 32.dp, vertical = 8.dp),
        )

        if (servers.isEmpty()) {
            Text(
                text = stringResource(R.string.remote_no_servers),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 32.dp, vertical = 16.dp),
            )
        } else {
            Card(
                modifier =
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp),
                colors =
                CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
            ) {
                Column {
                    servers.forEachIndexed { index, server ->
                        val shape =
                            when {
                                servers.size == 1 -> RoundedCornerShape(12.dp)
                                index == 0 -> RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp)
                                index == servers.size - 1 ->
                                    RoundedCornerShape(bottomStart = 12.dp, bottomEnd = 12.dp)

                                else -> RoundedCornerShape(0.dp)
                            }
                        var showMenu by remember { mutableStateOf(false) }
                        Box {
                            ListItem(
                                headlineContent = {
                                    Text(
                                        server.displayName,
                                        style = MaterialTheme.typography.bodyLarge,
                                    )
                                },
                                supportingContent =
                                if (server.name.isNotEmpty()) {
                                    {
                                        Text(
                                            server.url,
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                } else {
                                    null
                                },
                                trailingContent =
                                if (activeRemoteServer?.id == server.id) {
                                    {
                                        Icon(
                                            imageVector = Icons.Default.CheckCircle,
                                            contentDescription = null,
                                            tint = MaterialTheme.colorScheme.primary,
                                        )
                                    }
                                } else {
                                    null
                                },
                                modifier =
                                Modifier
                                    .clip(shape)
                                    .combinedClickable(
                                        onClick = {
                                            navController.navigate(
                                                "settings/remote_control/edit/${server.id}",
                                            )
                                        },
                                        onLongClick = { showMenu = true },
                                    ),
                                colors =
                                ListItemDefaults.colors(
                                    containerColor = Color.Transparent,
                                ),
                            )
                            DropdownMenu(
                                expanded = showMenu,
                                onDismissRequest = { showMenu = false },
                            ) {
                                DropdownMenuItem(
                                    text = { Text(stringResource(R.string.edit)) },
                                    leadingIcon = {
                                        Icon(Icons.Outlined.Edit, contentDescription = null)
                                    },
                                    onClick = {
                                        showMenu = false
                                        navController.navigate(
                                            "settings/remote_control/edit/${server.id}",
                                        )
                                    },
                                )
                                DropdownMenuItem(
                                    text = {
                                        Text(
                                            stringResource(R.string.menu_delete),
                                            color = MaterialTheme.colorScheme.error,
                                        )
                                    },
                                    leadingIcon = {
                                        Icon(
                                            Icons.Outlined.Delete,
                                            contentDescription = null,
                                            tint = MaterialTheme.colorScheme.error,
                                        )
                                    },
                                    onClick = {
                                        showMenu = false
                                        onDelete(server)
                                    },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

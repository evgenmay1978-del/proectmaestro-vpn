package com.maestrovpn.tv.compose.component

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.LinkOff
import androidx.compose.material.icons.outlined.Cable
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen

// Mirrors the remote status pill of the Apple clients: server name (or
// connecting state), groups/connections shortcuts, the remote service uptime,
// and a disconnect button.
@Composable
fun RemoteStatusBar(
    visible: Boolean,
    serverName: String,
    isConnected: Boolean,
    startTime: Long?,
    groupsCount: Int,
    hasGroups: Boolean,
    onGroupsClick: () -> Unit,
    connectionsCount: Int,
    onConnectionsClick: () -> Unit,
    onDisconnectClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    AnimatedVisibility(
        visible = visible,
        enter = slideInVertically { it } + fadeIn(),
        exit = slideOutVertically { it } + fadeOut(),
        modifier = modifier,
    ) {
        if (!rememberIsTv()) {
            // ── PHONE: Dark-Fantasy carved-wood/bronze status bar ──
            FantasyRemoteStatusBar(
                serverName = serverName,
                isConnected = isConnected,
                startTime = startTime,
                groupsCount = groupsCount,
                hasGroups = hasGroups,
                onGroupsClick = onGroupsClick,
                connectionsCount = connectionsCount,
                onConnectionsClick = onConnectionsClick,
                onDisconnectClick = onDisconnectClick,
            )
        } else {
            // ── TV: original Material bar (unchanged — live 1GB fleet) ──
            Surface(
                modifier = Modifier.fillMaxWidth(),
                color = MaterialTheme.colorScheme.surfaceContainer,
                tonalElevation = 3.dp,
            ) {
                Row(
                    modifier =
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text =
                        if (isConnected) {
                            serverName
                        } else {
                            stringResource(R.string.remote_connecting)
                        },
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        modifier = Modifier.weight(1f),
                    )

                    if (isConnected) {
                        Row(
                            modifier =
                            Modifier
                                .clip(RoundedCornerShape(8.dp))
                                .background(MaterialTheme.colorScheme.secondaryContainer)
                                .clickable(onClick = onConnectionsClick)
                                .padding(horizontal = 12.dp, vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.Center,
                        ) {
                            Text(
                                text = connectionsCount.toString(),
                                style = MaterialTheme.typography.labelLarge,
                                fontWeight = FontWeight.Medium,
                                color = MaterialTheme.colorScheme.onSecondaryContainer,
                            )
                            Spacer(modifier = Modifier.width(4.dp))
                            Icon(
                                imageVector = Icons.Outlined.Cable,
                                contentDescription = stringResource(R.string.title_connections),
                                modifier = Modifier.size(18.dp),
                                tint = MaterialTheme.colorScheme.onSecondaryContainer,
                            )
                        }

                        if (hasGroups) {
                            Row(
                                modifier =
                                Modifier
                                    .clip(RoundedCornerShape(8.dp))
                                    .background(MaterialTheme.colorScheme.secondaryContainer)
                                    .clickable(onClick = onGroupsClick)
                                    .padding(horizontal = 12.dp, vertical = 8.dp),
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.Center,
                            ) {
                                Text(
                                    text = groupsCount.toString(),
                                    style = MaterialTheme.typography.labelLarge,
                                    fontWeight = FontWeight.Medium,
                                    color = MaterialTheme.colorScheme.onSecondaryContainer,
                                )
                                Spacer(modifier = Modifier.width(4.dp))
                                Icon(
                                    imageVector = Icons.Default.Folder,
                                    contentDescription = stringResource(R.string.title_groups),
                                    modifier = Modifier.size(18.dp),
                                    tint = MaterialTheme.colorScheme.onSecondaryContainer,
                                )
                            }
                        }
                    }

                    Row(
                        modifier =
                        Modifier
                            .clip(RoundedCornerShape(8.dp))
                            .background(MaterialTheme.colorScheme.primaryContainer)
                            .clickable(onClick = onDisconnectClick)
                            .padding(horizontal = 12.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.Center,
                    ) {
                        if (isConnected && startTime != null) {
                            UptimeText(startTime = startTime)
                            Spacer(modifier = Modifier.width(4.dp))
                        }
                        Icon(
                            imageVector = Icons.Default.LinkOff,
                            contentDescription = stringResource(R.string.remote_disconnect),
                            modifier = Modifier.size(18.dp),
                            tint = MaterialTheme.colorScheme.onPrimaryContainer,
                        )
                    }
                }
            }
        }
    }
}

/**
 * PHONE-only Dark-Fantasy variant of the remote status bar. Same logic/callbacks as the TV
 * Material bar — only the visual skin changes: the whole strip becomes a carved-wood plaque
 * framed in aged bronze (`frame_bar`), and each shortcut/disconnect pill is a bronze
 * `frame_button` tile. Disconnect uses the warm (selected) bronze + orange accent.
 */
@Composable
private fun FantasyRemoteStatusBar(
    serverName: String,
    isConnected: Boolean,
    startTime: Long?,
    groupsCount: Int,
    hasGroups: Boolean,
    onGroupsClick: () -> Unit,
    connectionsCount: Int,
    onConnectionsClick: () -> Unit,
    onDisconnectClick: () -> Unit,
) {
    Row(
        modifier =
        Modifier
            .fillMaxWidth()
            .fantasyFrame(R.drawable.frame_bar)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text =
            if (isConnected) {
                serverName
            } else {
                stringResource(R.string.remote_connecting)
            },
            fontWeight = FontWeight.Bold,
            fontSize = 16.sp,
            color = Color(0xFFF1EEE6),
            maxLines = 1,
            modifier = Modifier.weight(1f),
        )

        if (isConnected) {
            FantasyStatusPill(onClick = onConnectionsClick) {
                Text(
                    text = connectionsCount.toString(),
                    fontWeight = FontWeight.Bold,
                    fontSize = 14.sp,
                    color = Color(0xFFF1EEE6),
                )
                Spacer(modifier = Modifier.width(5.dp))
                Icon(
                    imageVector = Icons.Outlined.Cable,
                    contentDescription = stringResource(R.string.title_connections),
                    modifier = Modifier.size(18.dp),
                    tint = NeonGreen,
                )
            }

            if (hasGroups) {
                FantasyStatusPill(onClick = onGroupsClick) {
                    Text(
                        text = groupsCount.toString(),
                        fontWeight = FontWeight.Bold,
                        fontSize = 14.sp,
                        color = Color(0xFFF1EEE6),
                    )
                    Spacer(modifier = Modifier.width(5.dp))
                    Icon(
                        imageVector = Icons.Default.Folder,
                        contentDescription = stringResource(R.string.title_groups),
                        modifier = Modifier.size(18.dp),
                        tint = NeonGreen,
                    )
                }
            }
        }

        // Disconnect = warm (selected) bronze frame + orange accent.
        FantasyStatusPill(onClick = onDisconnectClick, selected = true) {
            if (isConnected && startTime != null) {
                UptimeText(startTime = startTime)
                Spacer(modifier = Modifier.width(5.dp))
            }
            Icon(
                imageVector = Icons.Default.LinkOff,
                contentDescription = stringResource(R.string.remote_disconnect),
                modifier = Modifier.size(18.dp),
                tint = MaestroOrange,
            )
        }
    }
}

/**
 * A single clickable bronze pill (`frame_button` 9-patch) holding a number + icon or the
 * uptime + disconnect glyph. [selected] warms the bronze toward amber (the disconnect tile).
 */
@Composable
private fun FantasyStatusPill(
    onClick: () -> Unit,
    selected: Boolean = false,
    content: @Composable () -> Unit,
) {
    Row(
        modifier =
        Modifier
            .clickable(onClick = onClick)
            .fantasyFrame(R.drawable.frame_button, selected)
            .padding(horizontal = 14.dp, vertical = 9.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Center,
    ) {
        content()
    }
}

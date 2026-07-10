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
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.outlined.Cable
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
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
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.constant.Status
import kotlinx.coroutines.delay

@Composable
fun ServiceStatusBar(
    visible: Boolean,
    serviceStatus: Status,
    startTime: Long?,
    groupsCount: Int,
    hasGroups: Boolean,
    onGroupsClick: () -> Unit,
    connectionsCount: Int,
    onConnectionsClick: () -> Unit,
    onStopClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    AnimatedVisibility(
        visible = visible,
        enter = slideInVertically { it } + fadeIn(),
        exit = slideOutVertically { it } + fadeOut(),
        modifier = modifier,
    ) {
        // ── Dark-Fantasy carved-wood status bar (phone + TV), aged-bronze frame_bar ──
        FantasyStatusBar(
            serviceStatus = serviceStatus,
            startTime = startTime,
            groupsCount = groupsCount,
            hasGroups = hasGroups,
            onGroupsClick = onGroupsClick,
            connectionsCount = connectionsCount,
            onConnectionsClick = onConnectionsClick,
            onStopClick = onStopClick,
        )
    }
}

/**
 * Dark-Fantasy content of [ServiceStatusBar] (phone + TV): a carved-wood bar framed in aged
 * bronze (`frame_bar` 9-patch), emerald status text + gold counters.
 */
@Composable
private fun FantasyStatusBar(
    serviceStatus: Status,
    startTime: Long?,
    groupsCount: Int,
    hasGroups: Boolean,
    onGroupsClick: () -> Unit,
    connectionsCount: Int,
    onConnectionsClick: () -> Unit,
    onStopClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .fantasyFrame(R.drawable.frame_bar)
            .padding(horizontal = 18.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        // Status text — emerald engraving
        Text(
            text = when (serviceStatus) {
                Status.Starting -> stringResource(R.string.status_starting)
                Status.Started -> stringResource(R.string.status_started)
                Status.Stopping -> stringResource(R.string.status_stopping)
                else -> ""
            },
            color = NeonGreen,
            fontWeight = FontWeight.SemiBold,
            fontSize = 15.sp,
            modifier = Modifier.weight(1f),
        )

        // Connections chip
        FantasyStatChip(
            onClick = onConnectionsClick,
            text = connectionsCount.toString(),
            icon = Icons.Outlined.Cable,
            contentDescription = stringResource(R.string.title_connections),
        )

        // Groups chip (only show if hasGroups)
        if (hasGroups) {
            FantasyStatChip(
                onClick = onGroupsClick,
                text = groupsCount.toString(),
                icon = Icons.Default.Folder,
                contentDescription = stringResource(R.string.title_groups),
            )
        }

        // Stop chip — warm amber accent, uptime in gold
        Row(
            modifier = Modifier
                .clip(RoundedCornerShape(10.dp))
                .clickable(onClick = onStopClick)
                .background(Color(0x33FF6A3C))
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.Center,
        ) {
            if (startTime != null) {
                FantasyUptimeText(startTime = startTime)
                Spacer(modifier = Modifier.width(4.dp))
            }
            Icon(
                imageVector = Icons.Default.Stop,
                contentDescription = stringResource(R.string.stop),
                modifier = Modifier.size(18.dp),
                tint = GoldHi,
            )
        }
    }
}

/** Phone-only counter chip: gold count + emerald rune-icon on a dim carved-wood pill. */
@Composable
private fun FantasyStatChip(
    onClick: () -> Unit,
    text: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(10.dp))
            .clickable(onClick = onClick)
            .background(Color(0x33120A00))
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Center,
    ) {
        Text(
            text = text,
            color = GoldHi,
            fontWeight = FontWeight.SemiBold,
            fontSize = 14.sp,
        )
        Spacer(modifier = Modifier.width(4.dp))
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            modifier = Modifier.size(18.dp),
            tint = NeonGreen,
        )
    }
}

/**
 * Fantasy uptime label — IDENTICAL timing/formatting logic to [UptimeText], only the color is gold
 * to match the fantasy skin. Kept as a private copy so the public [UptimeText] (used elsewhere)
 * stays untouched.
 */
@Composable
private fun FantasyUptimeText(startTime: Long, modifier: Modifier = Modifier) {
    var currentTime by remember { mutableLongStateOf(System.currentTimeMillis()) }

    LaunchedEffect(startTime) {
        while (true) {
            delay(1000)
            currentTime = System.currentTimeMillis()
        }
    }

    val elapsedSeconds = ((currentTime - startTime) / 1000).coerceAtLeast(0)
    val hours = elapsedSeconds / 3600
    val minutes = (elapsedSeconds % 3600) / 60
    val seconds = elapsedSeconds % 60

    val formattedTime =
        if (hours > 0) {
            String.format("%d:%02d:%02d", hours, minutes, seconds)
        } else {
            String.format("%d:%02d", minutes, seconds)
        }

    Text(
        text = formattedTime,
        color = GoldMid,
        fontWeight = FontWeight.SemiBold,
        fontSize = 14.sp,
        modifier = modifier,
    )
}

@Composable
fun UptimeText(startTime: Long, modifier: Modifier = Modifier) {
    var currentTime by remember { mutableLongStateOf(System.currentTimeMillis()) }

    LaunchedEffect(startTime) {
        while (true) {
            delay(1000)
            currentTime = System.currentTimeMillis()
        }
    }

    val elapsedSeconds = ((currentTime - startTime) / 1000).coerceAtLeast(0)
    val hours = elapsedSeconds / 3600
    val minutes = (elapsedSeconds % 3600) / 60
    val seconds = elapsedSeconds % 60

    val formattedTime =
        if (hours > 0) {
            String.format("%d:%02d:%02d", hours, minutes, seconds)
        } else {
            String.format("%d:%02d", minutes, seconds)
        }

    Text(
        text = formattedTime,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Medium,
        color = MaterialTheme.colorScheme.onPrimaryContainer,
        modifier = modifier,
    )
}

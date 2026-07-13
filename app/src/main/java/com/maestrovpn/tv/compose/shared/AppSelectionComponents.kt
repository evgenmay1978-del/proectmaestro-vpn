package com.maestrovpn.tv.compose.shared

import android.Manifest
import android.content.pm.ApplicationInfo
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.drawable.BitmapDrawable
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.FantasyToggle
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen

enum class SortMode {
    NAME,
    PACKAGE_NAME,
    UID,
    INSTALL_TIME,
    UPDATE_TIME,
}

class PackageCache(
    private val packageInfo: PackageInfo,
    private val appInfo: ApplicationInfo,
    private val packageManager: PackageManager,
) {
    val packageName: String get() = packageInfo.packageName

    val uid: Int get() = appInfo.uid

    val installTime: Long get() = packageInfo.firstInstallTime
    val updateTime: Long get() = packageInfo.lastUpdateTime
    val isSystem: Boolean get() = appInfo.flags and ApplicationInfo.FLAG_SYSTEM == 1
    val isOffline: Boolean
        get() = packageInfo.requestedPermissions?.contains(Manifest.permission.INTERNET) != true
    val isDisabled: Boolean get() = appInfo.flags and ApplicationInfo.FLAG_INSTALLED == 0

    val applicationIcon by lazy {
        val drawable = appInfo.loadIcon(packageManager)
        val bitmap =
            if (drawable is BitmapDrawable) {
                drawable.bitmap
            } else {
                val imageBitmap =
                    Bitmap.createBitmap(
                        drawable.intrinsicWidth.coerceAtLeast(1),
                        drawable.intrinsicHeight.coerceAtLeast(1),
                        Bitmap.Config.ARGB_8888,
                    )
                val canvas = Canvas(imageBitmap)
                drawable.setBounds(0, 0, canvas.width, canvas.height)
                drawable.draw(canvas)
                imageBitmap
            }
        bitmap.asImageBitmap()
    }

    val applicationLabel by lazy {
        appInfo.loadLabel(packageManager).toString()
    }

    val info: PackageInfo get() = packageInfo
}

fun buildDisplayPackages(
    packages: List<PackageCache>,
    selectedUids: Set<Int> = emptySet(),
    selectedFirst: Boolean = false,
    hideSystemApps: Boolean,
    hideOfflineApps: Boolean,
    hideDisabledApps: Boolean,
    sortMode: SortMode,
    sortReverse: Boolean,
): List<PackageCache> {
    val displayPackages =
        packages.filter { packageCache ->
            if (hideSystemApps && packageCache.isSystem) {
                return@filter false
            }
            if (hideOfflineApps && packageCache.isOffline) {
                return@filter false
            }
            if (hideDisabledApps && packageCache.isDisabled) {
                return@filter false
            }
            true
        }
    val sortComparator =
        Comparator<PackageCache> { left, right ->
            if (selectedFirst) {
                val selectedCompare =
                    compareValues(
                        !selectedUids.contains(left.uid),
                        !selectedUids.contains(right.uid),
                    )
                if (selectedCompare != 0) {
                    return@Comparator selectedCompare
                }
            }
            val value =
                when (sortMode) {
                    SortMode.NAME -> compareValues(left.applicationLabel, right.applicationLabel)
                    SortMode.PACKAGE_NAME -> compareValues(left.packageName, right.packageName)
                    SortMode.UID -> compareValues(left.uid, right.uid)
                    SortMode.INSTALL_TIME -> compareValues(left.installTime, right.installTime)
                    SortMode.UPDATE_TIME -> compareValues(left.updateTime, right.updateTime)
                }
            if (sortReverse) -value else value
        }
    return displayPackages.sortedWith(sortComparator)
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun AppSelectionCard(
    packageCache: PackageCache,
    selected: Boolean,
    onToggle: (Boolean) -> Unit,
    enableCopyActions: Boolean = true,
    onCopyLabel: (() -> Unit)? = null,
    onCopyPackage: (() -> Unit)? = null,
    onCopyUid: (() -> Unit)? = null,
) {
    var showContextMenu by remember { mutableStateOf(false) }
    var showCopyMenu by remember { mutableStateOf(false) }
    val isTv = rememberIsTv()
    var isFocused by remember { mutableStateOf(false) }
    val tvShape = RoundedCornerShape(8.dp)
    val clickMod =
        if (enableCopyActions) {
            Modifier.combinedClickable(
                onClick = { onToggle(!selected) },
                onLongClick = { showContextMenu = true },
            )
        } else {
            Modifier.clickable { onToggle(!selected) }
        }

    // Shared row content. TV wraps it in the clean dark list surface; phone keeps the established art frame
    // and wood/gold material.
    val rowContent: @Composable () -> Unit = {
        Row(
            modifier = Modifier.padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Image(
                bitmap = packageCache.applicationIcon,
                contentDescription = stringResource(R.string.content_description_app_icon),
                modifier = Modifier.size(40.dp),
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = packageCache.applicationLabel,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    color = if (isTv) Color(0xFFF4F4EF) else Color(0xFFF1EEE6),
                )
                Text(
                    text = "${packageCache.packageName} (${packageCache.uid})",
                    style = MaterialTheme.typography.bodySmall,
                    color = if (isTv) Color(0xFFA8B7AF) else GoldMid.copy(alpha = 0.8f),
                    softWrap = true,
                )
            }
            FantasyToggle(checked = selected, onCheckedChange = { onToggle(it) })
        }
    }

    Box {
        if (isTv) {
            val borderColor =
                when {
                    isFocused -> Color(0xFFFFC857)
                    selected -> NeonGreen.copy(alpha = 0.72f)
                    else -> Color(0xFF2B4940)
                }
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .onFocusChanged { isFocused = it.isFocused }
                    .clip(tvShape)
                    .background(if (selected) Color(0xFF203B2C) else Color(0xFF101B18))
                    .border(if (isFocused) 2.dp else 1.dp, borderColor, tvShape)
                    .then(clickMod),
            ) { rowContent() }
        } else {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .then(clickMod)
                    .then(
                        if (selected) {
                            Modifier.shadow(
                                elevation = 9.dp,
                                shape = RoundedCornerShape(18.dp),
                                clip = false,
                                ambientColor = NeonGreen,
                                spotColor = NeonGreen,
                            )
                        } else {
                            Modifier
                        },
                    )
                    .fantasyFrame(R.drawable.frame_bar)
                    .padding(6.dp),
            ) { rowContent() }
        }

        if (enableCopyActions) {
            DropdownMenu(
                expanded = showContextMenu,
                onDismissRequest = {
                    showContextMenu = false
                    showCopyMenu = false
                },
            ) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.per_app_proxy_action_copy)) },
                    onClick = { showCopyMenu = !showCopyMenu },
                    leadingIcon = {
                        Icon(
                            imageVector = Icons.Default.ContentCopy,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    },
                    trailingIcon = {
                        Icon(
                            imageVector =
                            if (showCopyMenu) {
                                Icons.Default.ExpandLess
                            } else {
                                Icons.Default.ExpandMore
                            },
                            contentDescription = null,
                        )
                    },
                )
                if (showCopyMenu) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.profile_name)) },
                        onClick = {
                            showContextMenu = false
                            showCopyMenu = false
                            onCopyLabel?.invoke()
                        },
                        leadingIcon = {
                            Icon(
                                imageVector = Icons.Default.ContentCopy,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.padding(start = 24.dp),
                            )
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.per_app_proxy_action_copy_package_name)) },
                        onClick = {
                            showContextMenu = false
                            showCopyMenu = false
                            onCopyPackage?.invoke()
                        },
                        leadingIcon = {
                            Icon(
                                imageVector = Icons.Default.ContentCopy,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.padding(start = 24.dp),
                            )
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.per_app_proxy_action_copy_uid)) },
                        onClick = {
                            showContextMenu = false
                            showCopyMenu = false
                            onCopyUid?.invoke()
                        },
                        leadingIcon = {
                            Icon(
                                imageVector = Icons.Default.ContentCopy,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.padding(start = 24.dp),
                            )
                        },
                    )
                }
            }
        }
    }
}

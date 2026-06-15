package com.maestrovpn.tv.compose

import android.app.UiModeManager
import android.content.Context
import android.content.pm.PackageManager
import android.content.res.Configuration
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * True on Android TV / leanback devices (D-pad navigation), false on touch
 * phones and tablets. The one universal APK uses this to pick overscan-safe
 * 10-foot padding on TV vs tight padding on a handset, and to suppress the
 * launch focus-ring / donor chrome that only makes sense with a remote.
 */
@Composable
fun rememberIsTv(): Boolean {
    val context = LocalContext.current
    return remember {
        val pm = context.packageManager
        val uiMode = context.getSystemService(Context.UI_MODE_SERVICE) as? UiModeManager
        pm.hasSystemFeature(PackageManager.FEATURE_LEANBACK) ||
            pm.hasSystemFeature("android.hardware.type.television") ||
            uiMode?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION
    }
}

/** Outer screen padding: overscan-safe on TV, tight on a phone. */
@Composable
fun screenPadding(isTv: Boolean): Dp = if (isTv) 48.dp else 20.dp

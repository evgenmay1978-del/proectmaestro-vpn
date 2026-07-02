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
    return remember { isTelevision(context) }
}

/**
 * Non-Composable form-factor check for use outside Compose (Activity / Service). True on Android
 * TV / leanback devices, false on phones and tablets. Used e.g. to offer the quick-settings tile
 * only where a notification shade with QS tiles exists (phones), never on TV.
 */
fun isTelevision(context: Context): Boolean {
    val pm = context.packageManager
    val uiMode = context.getSystemService(Context.UI_MODE_SERVICE) as? UiModeManager
    return pm.hasSystemFeature(PackageManager.FEATURE_LEANBACK) ||
        pm.hasSystemFeature("android.hardware.type.television") ||
        uiMode?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION
}

/**
 * True on weak / low-RAM devices (≈1GB Android-TV boxes like Sony/TCL). The universal APK
 * reads this to drop the heaviest START-screen work — the perpetual 3D-logo spin and the
 * per-frame animated background — to a static render, so a weak GPU/CPU stops thrashing.
 * Gated UI only; NEVER touches the VPN tunnel / libbox.
 */
@Composable
fun rememberIsLowRam(): Boolean {
    val context = LocalContext.current
    return remember {
        val am = context.getSystemService(Context.ACTIVITY_SERVICE) as? android.app.ActivityManager
        am?.isLowRamDevice == true || (am?.memoryClass ?: 256) <= 96
    }
}

/** Outer screen padding: overscan-safe on TV, tight on a phone. */
@Composable
fun screenPadding(isTv: Boolean): Dp = if (isTv) 48.dp else 20.dp

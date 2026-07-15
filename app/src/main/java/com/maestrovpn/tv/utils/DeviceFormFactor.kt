package com.maestrovpn.tv.utils

import android.app.UiModeManager
import android.content.Context
import android.content.pm.PackageManager
import android.content.res.Configuration

/** System form-factor detection shared by UI, networking, and service/runtime gates. */
object DeviceFormFactor {
    const val MOBILE = "mobile"
    const val TV = "tv"

    fun isTelevision(context: Context): Boolean {
        val pm = context.packageManager
        val uiMode = context.getSystemService(Context.UI_MODE_SERVICE) as? UiModeManager
        return pm.hasSystemFeature(PackageManager.FEATURE_LEANBACK) ||
            pm.hasSystemFeature("android.hardware.type.television") ||
            uiMode?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION
    }

    fun subscriptionPlatform(context: Context): String = if (isTelevision(context)) TV else MOBILE
}

package com.maestrovpn.tv.ktx

import android.content.Context
import android.net.Uri
import android.widget.Toast
import androidx.browser.customtabs.CustomTabColorSchemeParams
import androidx.browser.customtabs.CustomTabsIntent
import com.google.android.material.elevation.SurfaceColors

/**
 * Open [link], tolerating a device with no browser at all.
 *
 * Upstream assumes a phone, where a browser always exists. This app's primary platform is a bare
 * Android TV box, where it usually does NOT: CustomTabsIntent falls back to a plain ACTION_VIEW,
 * that finds no activity, and the ActivityNotFoundException propagates out of the click handler on
 * the main thread — killing the app. Reached from "Документация", "Исходный код", "Спонсор",
 * "Установить Shizuku", "Подробнее", the update dialog and the service notification's link.
 *
 * The app's own screens already knew this (TvEskizHome wraps the same call in runCatching); this
 * puts the guard in the shared helper so every caller inherits it, and tells the user why nothing
 * happened instead of failing silently.
 */
fun Context.launchCustomTab(link: String) {
    runCatching { launchCustomTabOrThrow(link) }.onFailure {
        Toast.makeText(this, "Не удалось открыть ссылку — на устройстве нет браузера", Toast.LENGTH_LONG).show()
    }
}

private fun Context.launchCustomTabOrThrow(link: String) {
    val color = SurfaceColors.SURFACE_2.getColor(this)
    CustomTabsIntent.Builder().apply {
        setColorScheme(CustomTabsIntent.COLOR_SCHEME_SYSTEM)
        setColorSchemeParams(
            CustomTabsIntent.COLOR_SCHEME_LIGHT,
            CustomTabColorSchemeParams.Builder().apply {
                setToolbarColor(color)
            }.build(),
        )
        setColorSchemeParams(
            CustomTabsIntent.COLOR_SCHEME_DARK,
            CustomTabColorSchemeParams.Builder().apply {
                setToolbarColor(color)
            }.build(),
        )
    }.build().launchUrl(this, Uri.parse(link))
}

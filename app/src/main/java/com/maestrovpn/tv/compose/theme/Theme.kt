package com.maestrovpn.tv.compose.theme

import android.app.Activity
import android.os.Build
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

private val DarkColorScheme =
    darkColorScheme(
        // One material language for remaining Material3 controls and dialogs:
        // phone-frame gold + warm carved wood, never the old blue/grey donor UI.
        primary = Color(0xFFE8C877),
        onPrimary = Color(0xFF241506),
        primaryContainer = Color(0xFF4A3218),
        onPrimaryContainer = Color(0xFFFFE6B0),
        secondary = Color(0xFF73D98A),
        onSecondary = Color(0xFF071B0C),
        secondaryContainer = Color(0xFF17351E),
        onSecondaryContainer = Color(0xFFB9F3C5),
        tertiary = MaestroOrange,
        onTertiary = Color(0xFF271300),
        background = Color(0xFF090603),
        onBackground = Color(0xFFF1EEE6),
        surface = Color(0xFF171008),
        onSurface = Color(0xFFF1EEE6),
        surfaceVariant = Color(0xFF2B1D10),
        onSurfaceVariant = Color(0xFFD5C5A8),
        surfaceContainerLowest = Color(0xFF0D0905),
        surfaceContainerLow = Color(0xFF171008),
        surfaceContainer = Color(0xFF21160C),
        surfaceContainerHigh = Color(0xFF2B1D10),
        surfaceContainerHighest = Color(0xFF352513),
        outline = Color(0xFFA87C3A),
        outlineVariant = Color(0xFF5C3F20),
        surfaceTint = Color.Transparent,
    )

private val LightColorScheme =
    lightColorScheme(
        primary = SingBoxPrimary,
        secondary = SingBoxPrimaryDark,
        tertiary = LogBlue,
    )

@Composable
fun SFATheme(
    // MaestroVPN is always its branded dark orange theme — not the system wallpaper.
    darkTheme: Boolean = true,
    dynamicColor: Boolean = false,
    content: @Composable () -> Unit,
) {
    val colorScheme =
        when {
            dynamicColor && Build.VERSION.SDK_INT >= 31 -> {
                val context = LocalContext.current
                if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
            }

            darkTheme -> DarkColorScheme
            else -> LightColorScheme
        }

    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as? Activity)?.window ?: return@SideEffect
            window.statusBarColor = colorScheme.surface.toArgb()
            window.navigationBarColor = colorScheme.background.toArgb()
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !darkTheme
                isAppearanceLightNavigationBars = !darkTheme
            }
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        typography = Typography,
        shapes = Shapes,
        content = content,
    )
}

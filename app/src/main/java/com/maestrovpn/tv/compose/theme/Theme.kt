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
        primary = MaestroOrange,
        onPrimary = Color(0xFF231200),
        primaryContainer = MaestroOrangeDark,
        onPrimaryContainer = Color(0xFFFFE2CC),
        secondary = MaestroSilver,
        onSecondary = Color(0xFF15151A),
        tertiary = MaestroOrangeLight,
        background = MaestroBg,
        onBackground = Color(0xFFECECEE),
        surface = MaestroSurface,
        onSurface = Color(0xFFECECEE),
        surfaceVariant = MaestroSurfaceHi,
        onSurfaceVariant = Color(0xFFC8C8CE),
        outline = Color(0xFF55555E),
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

package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/** Crown used by the approved subscription and renewal controls. */
val MaestroCrown: ImageVector = ImageVector.Builder("MaestroCrown", 24.dp, 24.dp, 24f, 24f).apply {
    path(fill = SolidColor(Color.White)) {
        moveTo(1f, 6f); lineTo(6.5f, 10f); lineTo(12f, 2f); lineTo(17.5f, 10f)
        lineTo(23f, 6f); lineTo(20f, 18f); lineTo(4f, 18f); close()
        moveTo(4f, 20f); lineTo(20f, 20f); lineTo(20f, 22f); lineTo(4f, 22f); close()
    }
}.build()

/**
 * Панель утверждённого тёмного дизайна (23.11.2026): плоская тёмная карточка с тонкой
 * золото-оттенённой обводкой вместо резной nine-patch пластины. Выбранная — светлее
 * и с яркой золотой обводкой; навигация — полупрозрачная, с большим радиусом.
 * Одно место покрывает все телефонные панели/кнопки/навигацию (и fantasyFrame на
 * телефоне, который делегирует сюда же); на ТВ этот модификатор не используется.
 */
fun Modifier.approvedMobilePanel(selected: Boolean = false, navigation: Boolean = false): Modifier = composed {
    val bg = when {
        navigation -> DarkPanel.copy(alpha = 0.92f)
        selected -> DarkPanel2
        else -> DarkPanel
    }
    val border = when {
        navigation -> DarkLineSoft
        selected -> Color(0x8CD9A441) // rgba(217,164,65,.55)
        else -> DarkLineSoft
    }
    val radius = if (navigation) DarkNavCorner else DarkPanelCorner
    this
        .background(bg, RoundedCornerShape(radius))
        .border(1.dp, border, RoundedCornerShape(radius))
}

@Composable
fun ApprovedMobileBackground(modifier: Modifier = Modifier) {
    // Утверждённый тёмный дизайн (23.11.2026): чистый тёмный фон #0d0b08 с лёгким
    // золотым свечением сверху и снизу (как в прототипе). Дерево (phone_home_wood)
    // и резные рейки/капитель из атласа здесь больше не рисуются.
    Box(modifier.background(DarkBg)) {
        Canvas(Modifier.fillMaxSize()) {
            drawRect(
                brush = Brush.radialGradient(
                    0f to Color(0x29D9A441), // rgba(217,164,65,.16)
                    1f to Color.Transparent,
                    center = Offset(size.width / 2f, -size.height * 0.06f),
                    radius = size.width * 0.9f,
                ),
            )
            drawRect(
                brush = Brush.radialGradient(
                    0f to Color(0x12D9A441), // rgba(217,164,65,.07)
                    1f to Color.Transparent,
                    center = Offset(size.width / 2f, size.height * 1.08f),
                    radius = size.width * 0.75f,
                ),
            )
        }
    }
}

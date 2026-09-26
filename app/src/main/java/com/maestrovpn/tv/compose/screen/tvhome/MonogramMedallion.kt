package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.painterResource
import com.maestrovpn.tv.R

/**
 * Статичный медальон «M». Ни ореола, ни другого белого света: владелец убрал его из дизайна
 * (26–27.09.2026) — кожа остаётся тёмной, живёт только изумрудная кромка ([MonogramGlow]).
 */
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        Image(
            painter = painterResource(
                if (connected) R.drawable.mobile_medallion_m_on else R.drawable.mobile_medallion_m_off,
            ),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
    }
}

// Кромка кольца считается от края кожи, а не «на глаз» (замер ассетов, 27.09.2026):
// отверстие рамы — 207/712 ширины hero-бокса, диск рисуется диаметром 570/712 (радиус 285/712),
// поэтому внутренняя кромка кольца = 207/285 радиуса бокса диска. Низ буквы «M» в ассете лежит
// на 0.698 радиуса, т.е. 199/285 — кольцо начинается ЗА буквой и не перекрывает её.
private const val MEDALLION_RING_INNER = 207f / 285f
private const val MEDALLION_RING_WIDTH = 16f / 285f

/** Изумруд кромки — средний цвет зелёной полосы из ассета владельца: rgb(23, 209, 65). */
private val MedallionEmerald = Color(0xFF17D141)

/**
 * Живая кромка медальона. Рисуется кодом (drawCircle + Stroke) вместо PNG: у прежнего ассета
 * вокруг полосы был белый bloom, который и засвечивал кожу — удаление слоя ореола этого не лечило.
 * Слой идёт ПОВЕРХ рамы: под ней орнамент перекрывает всё, что нарисовано за краем диска.
 */
@Composable
internal fun MonogramGlow(connected: Boolean, modifier: Modifier = Modifier) {
    if (!connected) return
    val transition = rememberInfiniteTransition(label = "monogramGlow")
    val ringAlpha by transition.animateFloat(
        initialValue = 0.92f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = keyframes {
                durationMillis = 1200
                0.85f at 0
                1f at 90
                0.90f at 170
                1f at 260
                0.93f at 420
                1f at 700
                0.97f at 1200
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "ringFlicker",
    )
    Canvas(modifier) {
        val boxRadius = size.minDimension / 2f
        val stroke = boxRadius * MEDALLION_RING_WIDTH
        drawCircle(
            color = MedallionEmerald.copy(alpha = ringAlpha),
            radius = boxRadius * MEDALLION_RING_INNER + stroke / 2f,
            style = Stroke(width = stroke),
        )
    }
}

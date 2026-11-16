package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.painterResource
import com.maestrovpn.tv.R

/**
 * Медальон «M» из двух слоёв (05.10.2026): диск без буквы + объёмная буква «M» ([MonogramSpin]).
 * Буква вынесена из ассета диска: в покое статична, при подключении вращается. Ни ореола, ни
 * другого белого света: владелец убрал его из дизайна (26–27.09.2026) — кожа остаётся тёмной,
 * живёт только изумрудная кромка ([MonogramGlow]).
 */
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        Image(
            painter = painterResource(
                if (connected) R.drawable.mobile_medallion_disc_on else R.drawable.mobile_medallion_disc_off,
            ),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
        )
        MonogramSpin(connected = connected, modifier = Modifier.fillMaxSize())
    }
}

// Кромка кольца считается по ассету диска (1024): кожа видна до r=328, внутренняя золотая
// волосяная линия 328–339, золотая полоса 345–369, край диска 372. Бокс медальона — 570,
// поэтому 1 единица ассета = 285/512 радиуса бокса. Изумрудная кромка ложится ровно на золотую
// полосу (345–369 → 192–205 бокса) и не выходит ни на кожу, ни на дерево.
private const val MEDALLION_RING_INNER = 192f / 285f
private const val MEDALLION_RING_WIDTH = 13f / 285f

// Кольцо рвётся на четырёх камнях рамы (N/E/S/W) — требование владельца 27.09.2026: камни не
// перекрываются. Замер по кадру «Отключено»: камень ≈34 px шириной на радиусе 213–237 px, это
// ≈±5° дуги; половинa разрыва 10° даёт по ~2° (≈8 px) запаса с каждой стороны камня.
private const val MEDALLION_RING_GAP_DEG = 10f

/** Изумруд кромки — средний цвет зелёной полосы из ассета владельца: rgb(23, 209, 65). */
private val MedallionEmerald = Color(0xFF17D141)

/**
 * Живая кромка медальона. Рисуется кодом (drawArc + Stroke) вместо PNG: у прежнего ассета вокруг
 * полосы был белый bloom, который и засвечивал кожу, — удаление слоя ореола этого не лечило.
 * Слой идёт ПОВЕРХ рамы: под ней орнамент перекрывает всё, что нарисовано за краем диска.
 *
 * 16.11.2026: кромка горит ровно — мерцание (ключевые кадры 0,85…1 по 1,2 с) убрано: световая
 * пульсация запрещена владельцем (10.09.2026) и не нравилась ему на экране. Вместо мерцания —
 * разовое движение при подключении: каждая из четырёх дуг прорисовывается от камней к середине
 * ([RING_DRAW_MS]), дальше кольцо неподвижно.
 */
@Composable
internal fun MonogramGlow(connected: Boolean, modifier: Modifier = Modifier) {
    if (!connected) return
    val drawn = remember { Animatable(0f) }
    LaunchedEffect(Unit) { drawn.animateTo(1f, tween(RING_DRAW_MS, easing = FastOutSlowInEasing)) }
    Canvas(modifier) {
        val boxRadius = size.minDimension / 2f
        val stroke = boxRadius * MEDALLION_RING_WIDTH
        val radius = boxRadius * MEDALLION_RING_INNER + stroke / 2f
        val arcTopLeft = Offset(center.x - radius, center.y - radius)
        val arcSize = Size(radius * 2f, radius * 2f)
        val sweep = 90f - 2f * MEDALLION_RING_GAP_DEG
        val grow = sweep / 2f * drawn.value
        for (quadrant in 0 until 4) {
            val from = 90f * quadrant + MEDALLION_RING_GAP_DEG
            // Два встречных отрезка: от камня в начале дуги и от камня в её конце.
            for (startAngle in floatArrayOf(from, from + sweep - grow)) {
                drawArc(
                    color = MedallionEmerald,
                    startAngle = startAngle,
                    sweepAngle = grow,
                    useCenter = false,
                    topLeft = arcTopLeft,
                    size = arcSize,
                    style = Stroke(width = stroke),
                )
            }
        }
    }
}

/** Прорисовка кольца при подключении, мс. */
internal const val RING_DRAW_MS = 700

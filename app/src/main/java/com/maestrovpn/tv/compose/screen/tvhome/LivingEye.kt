package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.FastOutLinearInEasing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.VectorConverter
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.res.imageResource
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.sin
import kotlin.random.Random

/**
 * Живой глаз в сокете медальона (только телефон). Арт = ПИКСЕЛИ мокапа владельца, разрезанные на
 * слои (home_eye_base = склера с дорисованной под радужкой тканью, home_eye_iris = радужка+зрачок,
 * home_plasma_disc = энергосфера отключённого состояния). Вся «жизнь» — только трансформации этих
 * слоёв: моргание/прищур (веки), блуждание взгляда + саккады, дыхание зрачка, влажные блики,
 * слежение за пальцем, вихрь плазмы на появление/растворение. В покое по центру композиция
 * пиксель-в-пиксель совпадает с запечённым фоном (проверено попиксельно при раскройке).
 * Никакой линейной механики: все движения на кривых с лёгкой случайностью.
 */
@Composable
fun LivingEye(
    connected: Boolean,
    eyeAlpha: () -> Float,          // кроссфейд фонов из TvHomeScreen (0=сфера, 1=глаз)
    touchDir: State<Offset?>,       // направление на палец в долях (-1..1) от центра сокета, null = нет касания
    modifier: Modifier = Modifier,
) {
    val eyeBase = ImageBitmap.imageResource(R.drawable.home_eye_base)
    val eyeIris = ImageBitmap.imageResource(R.drawable.home_eye_iris)
    val plasma = ImageBitmap.imageResource(R.drawable.home_plasma_disc)

    // взгляд в долях макс. сдвига (-1..1 по обеим осям, длина ≤1)
    val gaze = remember { Animatable(Offset.Zero, Offset.VectorConverter) }
    val lid = remember { Animatable(1f) }        // 1 = закрыт, 0 = открыт
    val squint = remember { Animatable(0f) }     // 0..1 лёгкий прищур (доля века)
    val dilate = remember { Animatable(0f) }     // 0 = зрачок как в арте, 1 = максимально расширен
    val greenGlint = remember { Animatable(0f) } // зелёный отблеск при подключении
    val spin = remember { Animatable(0f) }       // градусы вихря плазмы
    var breath by remember { mutableFloatStateOf(0f) }      // медленное «дыхание» зрачка ±
    var glintDrift by remember { mutableFloatStateOf(0f) }  // дрейф влажных бликов
    var watching by remember { mutableFloatStateOf(0f) }    // >0 = следим за пальцем/точкой (гасим блуждание)

    // ── реакция на палец: мгновенный перевод взгляда; после отпускания «наблюдаем» 2-3.5с ──
    LaunchedEffect(Unit) {
        coroutineScope {
            var release: Job? = null
            snapshotFlow { touchDir.value }.collect { t ->
                if (t != null) {
                    release?.cancel(); release = null
                    watching = 1f
                    launch { gaze.animateTo(t, tween(150, easing = FastOutSlowInEasing)) }
                } else if (watching > 0f) {
                    release = launch {
                        delay(Random.nextLong(2000, 3500))
                        watching = 0f
                    }
                }
            }
        }
    }

    // ── главная хореография: появление → жизнь → (на false) осмотр и растворение ──
    LaunchedEffect(connected) {
        if (connected) {
            // ПОЯВЛЕНИЕ: энергия закручивается, из глубины силуэт, веки открываются, зрачок фокусируется
            lid.snapTo(1f); dilate.snapTo(0f); gaze.snapTo(Offset.Zero); squint.snapTo(0f)
            launch { spin.animateTo(spin.value + 480f, tween(1000, easing = CubicBezierEasing(0.4f, 0f, 0.8f, 1f))) }
            delay(260)                                     // силуэт проступает под открывающимися веками
            lid.animateTo(0f, tween(680, easing = CubicBezierEasing(0.25f, 0f, 0.2f, 1f)))
            launch { dilate.animateTo(1f, tween(400, easing = FastOutSlowInEasing)); dilate.animateTo(0.30f, tween(650)) }
            launch { greenGlint.animateTo(1f, tween(260)); greenGlint.animateTo(0f, tween(950)) }
            delay(Random.nextLong(500, 1000))              // первый фокус на пользователе

            // ЖИЗНЬ — три параллельных контура, каждый со случайностью (цикл не повторяется)
            coroutineScope {
                launch { // моргание 3-8с, иногда двойное; веко чуть движется с взглядом само (в draw)
                    while (true) {
                        delay(Random.nextLong(3000, 8000))
                        blink(lid)
                        if (Random.nextFloat() < 0.24f) { delay(Random.nextLong(160, 320)); blink(lid) }
                    }
                }
                launch { // блуждание взгляда + саккады + возврат в центр
                    while (true) {
                        if (watching == 0f) {
                            val target = when {
                                Random.nextFloat() < 0.14f -> Offset.Zero                      // возврат в центр
                                Random.nextFloat() < 0.25f -> randomDir(0.55f, 0.9f)           // взгляд к краю
                                else -> randomDir(0.08f, 0.45f)                                // мягкое блуждание
                            }
                            gaze.animateTo(target, tween(Random.nextInt(320, 640), easing = FastOutSlowInEasing))
                            repeat(Random.nextInt(0, 3)) {                                     // саккады
                                if (watching != 0f) return@repeat
                                delay(Random.nextLong(120, 350))
                                val j = target + randomDir(0.02f, 0.06f)
                                gaze.animateTo(Offset(j.x.coerceIn(-1f, 1f), j.y.coerceIn(-1f, 1f)),
                                    tween(Random.nextInt(55, 95), easing = LinearOutSlowInEasing))
                            }
                        }
                        delay(Random.nextLong(500, 2200))
                    }
                }
                launch { // редкий прищур
                    while (true) {
                        delay(Random.nextLong(7000, 15000))
                        if (watching == 0f && Random.nextFloat() < 0.7f) {
                            squint.animateTo(0.22f + Random.nextFloat() * 0.10f, tween(260, easing = FastOutSlowInEasing))
                            delay(Random.nextLong(400, 900))
                            squint.animateTo(0f, tween(340, easing = FastOutSlowInEasing))
                        }
                    }
                }
                launch { // дыхание зрачка (реакция на свет) + дрейф влажных бликов — покадрово
                    val t0 = withFrameNanos { it }
                    while (true) {
                        withFrameNanos { f ->
                            val t = (f - t0) / 1_000_000_000f
                            breath = 0.045f * sin(t * 0.7f) + 0.03f * sin(t * 1.9f + 1.3f)
                            glintDrift = sin(t * 0.5f)
                        }
                    }
                }
            }
        } else {
            // ПОТЕРЯ СОЕДИНЕНИЯ: быстрый осмотр → моргнуть → зрачок сузился → вихрь растворяет
            if (eyeAlpha() > 0.05f) {
                launch { spin.animateTo(spin.value + 720f, tween(1200, easing = CubicBezierEasing(0.3f, 0f, 0.7f, 1f))) }
                repeat(3) {
                    gaze.animateTo(randomDir(0.5f, 0.9f), tween(130, easing = FastOutLinearInEasing))
                    delay(Random.nextLong(60, 140))
                }
                blink(lid)
                dilate.animateTo(0f, tween(200))
                gaze.animateTo(Offset.Zero, tween(300))
            }
            // после затухания вернуть вихрь в 0 (кратно 360°), чтобы покой = запечённый пиксель-в-пиксель
            delay(900)
            spin.snapTo(((spin.value % 360f) + 360f) % 360f)
            spin.animateTo(if (spin.value > 180f) 360f else 0f, tween(400))
            spin.snapTo(0f)
        }
    }

    Canvas(modifier) {
        val ea = eyeAlpha()
        val r = size.minDimension / 2f
        val c = Offset(size.width / 2f, size.height / 2f)

        // ── плазменный вихрь (отключено/переходы): его же пиксели, только вращение ──
        if (ea < 0.995f && (spin.value % 360f != 0f || ea > 0.005f)) {
            rotate(spin.value, c) {
                drawImage(plasma, dstOffset = IntOffset((c.x - r).toInt(), (c.y - r).toInt()),
                    dstSize = IntSize((2 * r).toInt(), (2 * r).toInt()),
                    alpha = 1f - ea, filterQuality = FilterQuality.Medium)
            }
        }
        if (ea < 0.005f) return@Canvas

        val socket = Path().apply { addOval(Rect(c.x - r, c.y - r, c.x + r, c.y + r)) }
        clipPath(socket) {
            // склера (под радужкой — дорисованная ткань, видна только при сдвиге взгляда)
            drawImage(eyeBase, dstOffset = IntOffset((c.x - r).toInt(), (c.y - r).toInt()),
                dstSize = IntSize((2 * r).toInt(), (2 * r).toInt()),
                alpha = ea, filterQuality = FilterQuality.Medium)

            // радужка+зрачок: покой = точное запечённое место (+7,+10 от центра сокета в осях арта 234)
            val maxShift = r * 0.115f
            val irisC = c + Offset(7f / 234f * r, 10f / 234f * r) +
                Offset(gaze.value.x * maxShift, gaze.value.y * maxShift)
            val irisR = r * (146f / 234f)
            drawImage(eyeIris, dstOffset = IntOffset((irisC.x - irisR).toInt(), (irisC.y - irisR).toInt()),
                dstSize = IntSize((2 * irisR).toInt(), (2 * irisR).toInt()),
                alpha = ea, filterQuality = FilterQuality.Medium)

            // зрачок: расширение поверх артового (арт = минимальный размер, только рост)
            val pupilR = r * (43f / 234f) * (1f + 0.38f * dilate.value + breath.coerceAtLeast(-0.04f))
            drawCircle(
                brush = Brush.radialGradient(
                    0f to Color(0xFF060604), 0.74f to Color(0xFF060604), 1f to Color(0x00060604),
                    center = irisC, radius = pupilR * 1.18f,
                ),
                radius = pupilR * 1.18f, center = irisC, alpha = ea,
            )

            // влажные блики: чуть противоходом взгляду + медленный дрейф (запечённые остаются на радужке)
            val g1 = irisC + Offset(-pupilR * 0.55f, -pupilR * 1.15f) -
                Offset(gaze.value.x, gaze.value.y) * (maxShift * 0.35f) + Offset(glintDrift * 2f, glintDrift * 1.2f)
            drawCircle(Color.White.copy(alpha = 0.22f * ea), radius = r * 0.030f, center = g1)
            drawCircle(Color.White.copy(alpha = 0.12f * ea), radius = r * 0.016f,
                center = irisC + Offset(pupilR * 0.9f, pupilR * 0.7f) - Offset(glintDrift * 1.5f, glintDrift * 0.8f))

            // зелёный отблеск подключения
            if (greenGlint.value > 0.01f) {
                drawCircle(
                    brush = Brush.radialGradient(
                        0.35f to Color(0x0034E67A), 0.8f to Color(0xFF34E67A).copy(alpha = 0.30f * greenGlint.value),
                        1f to Color.Transparent, center = c, radius = r,
                    ),
                    radius = r, center = c,
                )
            }

            // веки: верхнее ведущее (+ след взгляда вверх/вниз + прищур), нижнее — 40% хода
            val lidK = (lid.value + squint.value * 0.9f + abs(gaze.value.y) * 0.05f).coerceIn(0f, 1f)
            if (lidK > 0.002f) {
                val lidBrush = Brush.verticalGradient(
                    0f to Color(0xFF17110A), 0.8f to Color(0xFF241A0F), 1f to Color(0xFF312514),
                    startY = c.y - r, endY = c.y + r,
                )
                val upperEdge = c.y - r + (2f * r) * (0.56f * lidK)
                val upper = Path().apply {
                    moveTo(c.x - r, c.y - r); lineTo(c.x + r, c.y - r)
                    lineTo(c.x + r, upperEdge - r * 0.10f)
                    quadraticBezierTo(c.x, upperEdge + r * 0.16f, c.x - r, upperEdge - r * 0.10f)
                    close()
                }
                drawPath(upper, lidBrush, alpha = ea)
                drawPath(upper, Color(0xFF0B0805), alpha = 0.35f * ea, style = androidx.compose.ui.graphics.drawscope.Stroke(width = r * 0.012f))
                val lowerEdge = c.y + r - (2f * r) * (0.40f * 0.56f * lidK)
                val lower = Path().apply {
                    moveTo(c.x - r, c.y + r); lineTo(c.x + r, c.y + r)
                    lineTo(c.x + r, lowerEdge + r * 0.08f)
                    quadraticBezierTo(c.x, lowerEdge - r * 0.12f, c.x - r, lowerEdge + r * 0.08f)
                    close()
                }
                drawPath(lower, lidBrush, alpha = 0.92f * ea)
            }
        }
    }
}

/** Одно естественное моргание: быстро вниз, короткая пауза, мягче вверх. */
private suspend fun blink(lid: Animatable<Float, *>) {
    lid.animateTo(1f, tween(Random.nextInt(90, 120), easing = FastOutLinearInEasing))
    delay(Random.nextLong(25, 55))
    lid.animateTo(0f, tween(Random.nextInt(150, 210), easing = LinearOutSlowInEasing))
}

/** Случайное направление взгляда длиной в [rMin..rMax] долей от максимума. */
private fun randomDir(rMin: Float, rMax: Float): Offset {
    val ang = Random.nextFloat() * (2f * Math.PI.toFloat())
    val len = rMin + Random.nextFloat() * (rMax - rMin)
    return Offset(kotlin.math.cos(ang) * len, sin(ang) * len)
}

/** Ограничение вектора длиной 1 (для направления на палец). */
internal fun Offset.clampLen1(): Offset {
    val l = hypot(x, y)
    return if (l <= 1f || l == 0f) this else Offset(x / l, y / l)
}

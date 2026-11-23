package com.maestrovpn.tv.compose.screen.tvhome

import android.content.res.Resources
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
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
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.premium.PremiumGold
import com.maestrovpn.tv.compose.premium.PremiumGoldMuted
import kotlinx.coroutines.delay
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlin.math.sin

/**
 * Медальон-монетка (23.11.2026, утверждённый мобильный дизайн): вращается сама МОНЕТА
 * (диск + буква одним слоем), буква статична на ней с обеих сторон. В покое монетка живёт
 * медленным поворотом (как первый прототип: пауза в анфас, поворот до 56°, пауза, обратно);
 * при подключении — ровный оборот 3,6 с на круг. Золотое ребро монеты — полоса, чья ширина
 * следует |sin угла|: в анфас её нет, на ребре видна полностью.
 *
 * Буква — статичный объёмный кадр владельца (mobile_medallion_m_idle), та же геометрия
 * посадки, что у прежнего MonogramSpin. Отдельные кадры вращения буквы (72 шт.) больше не
 * нужны: вращалась бы буква, а не монета. На обороте та же M: буква симметрична слева
 * направо, диск симметричен (4 камня по осям), поэтому зеркальный тыл читается как та же
 * сторона. Новых отрендеренных кадров «диск+M» не потребовалось.
 */
@Composable
internal fun MonogramMedallion(connected: Boolean, modifier: Modifier = Modifier) {
    val angle = remember { Animatable(0f) }
    val density = LocalDensity.current
    LaunchedEffect(connected) {
        if (connected) {
            // Ровный непрерывный оборот монеты, 3,6 с на круг; буква статична на ней.
            while (true) {
                angle.animateTo(angle.value + 360f, tween(COIN_SPIN_PERIOD_MS, easing = LinearEasing))
                angle.snapTo(angle.value % 360f)
            }
        } else {
            // Плавно встать в анфас, затем медленный поворот монеты в покое.
            val settle = (angle.value / 360f).roundToInt() * 360f
            angle.animateTo(settle, tween(COIN_SETTLE_MS, easing = FastOutSlowInEasing))
            angle.snapTo(0f)
            while (true) {
                delay(COIN_IDLE_FACE_MS)
                angle.animateTo(COIN_IDLE_TURN_DEG, tween(COIN_IDLE_TURN_MS, easing = FastOutSlowInEasing))
                delay(COIN_IDLE_SIDE_MS)
                angle.animateTo(-COIN_IDLE_TURN_DEG, tween(COIN_IDLE_SWING_MS, easing = FastOutSlowInEasing))
                angle.animateTo(0f, tween(COIN_IDLE_RETURN_MS, easing = FastOutSlowInEasing))
            }
        }
    }
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        // Золотое ребро монеты: полоса по центру, ширина — доля толщины ребра от |sin угла|.
        Canvas(Modifier.fillMaxSize()) {
            val edge = abs(sin(angle.value * PI.toFloat() / 180f))
            val thickness = size.minDimension * COIN_EDGE_FRACTION * edge
            if (thickness >= 1f) {
                drawRoundRect(
                    brush = Brush.verticalGradient(
                        listOf(PremiumGold, PremiumGoldMuted, PremiumGold),
                    ),
                    topLeft = Offset((size.width - thickness) / 2f, 0f),
                    size = Size(thickness, size.height),
                    cornerRadius = CornerRadius(thickness / 2f, thickness / 2f),
                )
            }
        }
        // Лицо монеты: диск + буква одним слоем, поворачиваются вместе вокруг вертикали.
        Box(
            Modifier.fillMaxSize().graphicsLayer {
                rotationY = angle.value
                cameraDistance = COIN_CAMERA_DISTANCE * density.density
            },
            contentAlignment = Alignment.Center,
        ) {
            Image(
                painter = painterResource(
                    if (connected) R.drawable.mobile_medallion_disc_on else R.drawable.mobile_medallion_disc_off,
                ),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
            )
            MonogramLetter(Modifier.fillMaxSize())
        }
    }
}

/** Оборот монеты при подключении, мс на круг (как в утверждённом прототипе). */
private const val COIN_SPIN_PERIOD_MS = 3_600

/** Плавная остановка монеты в анфас при отключении, мс. */
private const val COIN_SETTLE_MS = 400

/** Поворот монеты в покое: ±56° с паузами в анфас (как первый прототип). */
private const val COIN_IDLE_TURN_DEG = 56f
private const val COIN_IDLE_FACE_MS = 1_800L
private const val COIN_IDLE_SIDE_MS = 1_200L
private const val COIN_IDLE_TURN_MS = 2_000
private const val COIN_IDLE_SWING_MS = 1_800
private const val COIN_IDLE_RETURN_MS = 1_400

/** Толщина ребра монеты, доля диаметра (как слои ребра в прототипе, ~9%). */
private const val COIN_EDGE_FRACTION = 0.09f

/** Дистанция камеры для поворота монеты: слабая перспектива, без искажения диска. */
private const val COIN_CAMERA_DISTANCE = 2000f

// Окно кадра буквы в долях бокса медальона: 128/1024, 280/1024, 768/1024, 544/1024
// (та же геометрия, что у прежнего MonogramSpin: буква встаёт на прежнее место без подгонки).
private const val FRAME_LEFT = 0.125f
private const val FRAME_TOP = 0.2734375f
private const val FRAME_WIDTH = 0.75f
private const val FRAME_HEIGHT = 0.53125f

// В кадрах буква шире диска: её нижние засечки расходятся до r=402 при радиусе диска 372
// (система ассета 1024), поэтому уменьшаем монограмму так, чтобы она целиком встала в кольцо
// (402*0.86 = 346), а клип по 360/1024 оставляем страховкой.
private const val FRAME_SCALE = 0.86f
private const val FRAME_CLIP_RADIUS = 360f / 1024f
private const val IDLE_SAMPLE = 1

private fun decodeFrame(resources: Resources, id: Int, sample: Int): ImageBitmap? = runCatching {
    BitmapFactory.decodeResource(
        resources,
        id,
        BitmapFactory.Options().apply {
            inPreferredConfig = Bitmap.Config.ARGB_8888
            inSampleSize = sample
        },
    ).asImageBitmap()
}.getOrNull()

/** Объёмная буква «M» отдельным слоем: статичный кадр владельца в полном разрешении. */
@Composable
private fun MonogramLetter(modifier: Modifier = Modifier) {
    val resources = LocalContext.current.resources
    val idle = remember(resources) { decodeFrame(resources, R.drawable.mobile_medallion_m_idle, IDLE_SAMPLE) }
    Canvas(modifier) {
        val bitmap = idle ?: return@Canvas
        // Масштабируем кадр вокруг центра диска: буква остаётся по центру, но влезает в кольцо.
        val centerX = size.width / 2f
        val centerY = size.height / 2f
        val left = centerX + (size.width * FRAME_LEFT - centerX) * FRAME_SCALE
        val top = centerY + (size.height * FRAME_TOP - centerY) * FRAME_SCALE
        val width = size.width * FRAME_WIDTH * FRAME_SCALE
        val height = size.height * FRAME_HEIGHT * FRAME_SCALE
        val box = minOf(size.width, size.height)
        val radius = box * FRAME_CLIP_RADIUS
        val disc = Path().apply {
            addOval(
                Rect(
                    Offset(size.width / 2f - radius, size.height / 2f - radius),
                    Size(radius * 2f, radius * 2f),
                ),
            )
        }
        clipPath(disc) {
            drawImage(
                image = bitmap,
                srcOffset = IntOffset.Zero,
                srcSize = IntSize(bitmap.width, bitmap.height),
                dstOffset = IntOffset(left.roundToInt(), top.roundToInt()),
                dstSize = IntSize(width.roundToInt().coerceAtLeast(1), height.roundToInt().coerceAtLeast(1)),
                filterQuality = FilterQuality.High,
            )
        }
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

package com.maestrovpn.tv.compose.screen.tvhome

import android.content.res.Resources
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
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
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.premium.DarkGreen
import kotlinx.coroutines.delay
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlin.math.sin

/**
 * Медальон-монетка (утверждённый тёмный дизайн 23.11.2026, прототип
 * «MaestroVPN Mobile — моё видение»): вращается сама МОНЕТА (диск + буква одним
 * слоем), буква статична на ней с обеих сторон. В покое монетка живёт медленным
 * поворотом (пауза в анфас, поворот до 56°, пауза, обратно); при подключении —
 * ровный оборот 3,6 с на круг, кольцо зеленеет. Диск рисуется кодом по прототипу
 * (тёмный радиальный диск + тонкие золотые кольца) — плоские PNG-диски со старой
 * кожаной текстурой и камнями больше не используются: владелец назвал монетку
 * в r706 мелкой и плоской.
 *
 * Буква — статичный объёмный кадр владельца (mobile_medallion_m_idle), посажена
 * КРУПНО, как в прототипе (кадр 73,02% ширины сцены, клип 44,5%). Отдельные кадры
 * вращения буквы (72 шт.) не нужны: вращалась бы буква, а не монета. На обороте
 * та же M: буква симметрична слева направо, диск симметричен, поэтому зеркальный
 * тыл читается как та же сторона. Новых отрендеренных кадров «диск+M» не требуется.
 */
@Composable
internal fun MonogramMedallion(
    connected: Boolean,
    connecting: Boolean = false,
    modifier: Modifier = Modifier,
) {
    val angle = remember { Animatable(0f) }
    val density = LocalDensity.current
    // Пульс яркости при подключении (прототип coinPulse, 1 с): белая вуаль над монетой.
    val pulse = remember { Animatable(0f) }
    LaunchedEffect(connecting) {
        if (connecting) {
            while (true) {
                pulse.animateTo(1f, tween(500, easing = FastOutSlowInEasing))
                pulse.animateTo(0f, tween(500, easing = FastOutSlowInEasing))
            }
        } else {
            pulse.snapTo(0f)
        }
    }
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
        // Золотое ребро монеты с насечкой + зелёное свечение при подключении (под лицом).
        Canvas(Modifier.fillMaxSize()) {
            if (connected) {
                drawCircle(
                    brush = Brush.radialGradient(
                        0f to DarkGreen.copy(alpha = 0.26f),
                        1f to Color.Transparent,
                        center = center,
                        radius = size.minDimension * 0.62f,
                    ),
                    radius = size.minDimension * 0.62f,
                    center = center,
                )
            }
            val edge = abs(sin(angle.value * PI.toFloat() / 180f))
            val thickness = size.minDimension * COIN_EDGE_FRACTION * edge
            if (thickness >= 1f) {
                val left = (size.width - thickness) / 2f
                // Насечка на торце: чередование золотых оттенков поперёк полосы.
                drawRoundRect(
                    brush = Brush.horizontalGradient(
                        0.00f to Color(0xFFF0CD72),
                        0.20f to Color(0xFFCAA02F),
                        0.35f to Color(0xFF8A5F14),
                        0.50f to Color(0xFFDBB153),
                        0.65f to Color(0xFFA87F22),
                        0.80f to Color(0xFF75520F),
                        1.00f to Color(0xFFF0CD72),
                        startX = left,
                        endX = left + thickness,
                    ),
                    topLeft = Offset(left, 0f),
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
            Canvas(Modifier.fillMaxSize()) { drawDiscFace(connected) }
            MonogramLetter(Modifier.fillMaxSize())
        }
        // Пульс яркости при подключении: белая вуаль поверх монеты.
        if (connecting && pulse.value > 0f) {
            Canvas(Modifier.fillMaxSize()) {
                drawCircle(
                    Color.White.copy(alpha = 0.14f * pulse.value),
                    radius = size.minDimension / 2f,
                    center = center,
                )
            }
        }
    }
}

/** Лицо диска по прототипу: тёмный радиальный диск, блик сверху, тонкие золотые кольца. */
private fun DrawScope.drawDiscFace(connected: Boolean) {
    val r = size.minDimension / 2f
    val center = Offset(size.width / 2f, size.height / 2f)
    drawCircle(
        brush = Brush.radialGradient(
            0f to Color(0xFF251C10),
            0.58f to Color(0xFF171208),
            1f to Color(0xFF0C0905),
            center = center,
            radius = r,
        ),
        radius = r,
        center = center,
    )
    // Верхний блик.
    drawCircle(
        brush = Brush.radialGradient(
            0f to Color(0x2BF6DA8A), // rgba(246,218,138,.17)
            1f to Color.Transparent,
            center = Offset(center.x, center.y - r * 0.55f),
            radius = r * 0.9f,
        ),
        radius = r,
        center = center,
    )
    // Внутренняя тень сверху/снизу.
    val oval = Path().apply { addOval(Rect(center - Offset(r, r), Size(2f * r, 2f * r))) }
    clipPath(oval) {
        drawRect(
            brush = Brush.verticalGradient(
                0f to Color(0x80000000),
                0.25f to Color.Transparent,
                0.75f to Color.Transparent,
                1f to Color(0xA6000000),
            ),
        )
    }
    // Кольца ободка (в прототипе — inset box-shadow): внешнее золотое (зелёное при
    // подключении), тёмный зазор, тонкая золотая волосяная линия, кромка по самому краю.
    val ring = if (connected) DarkGreen else Color(0xFFC9992E)
    val ringHair = if (connected) DarkGreen.copy(alpha = 0.42f) else Color(0x73D9A441)
    val ringRim = if (connected) DarkGreen.copy(alpha = 0.30f) else Color(0x42D9A441)
    drawCircle(color = ring, radius = r - 0.75.dp.toPx(), center = center, style = Stroke(width = 1.5.dp.toPx()))
    drawCircle(color = Color(0xEB120C03), radius = r - 2.dp.toPx(), center = center, style = Stroke(width = 1.dp.toPx()))
    drawCircle(color = ringHair, radius = r - 2.5.dp.toPx(), center = center, style = Stroke(width = 0.75.dp.toPx()))
    drawCircle(color = ringRim, radius = r - 0.25.dp.toPx(), center = center, style = Stroke(width = 0.5.dp.toPx()))
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

// Посадка буквы по прототипу: кадр 73,02% ширины сцены, верх 22,29%, левый край 12,78%,
// высота кадра 51,73% (768×544). Центр чернил буквы при этом — точно в центре монеты.
// Масштаб 1,0: уменьшать букву больше не нужно — клип 44,5% оставляет зазор ~14 px до кольца.
private const val FRAME_LEFT = 0.1278f
private const val FRAME_TOP = 0.2229f
private const val FRAME_WIDTH = 0.7302f
private const val FRAME_HEIGHT = 0.5173f
private const val FRAME_SCALE = 1f
private const val FRAME_CLIP_RADIUS = 0.445f
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
            // Тень буквы на диске (прототип: drop-shadow под лицевой гранью).
            drawImage(
                image = bitmap,
                srcOffset = IntOffset.Zero,
                srcSize = IntSize(bitmap.width, bitmap.height),
                dstOffset = IntOffset((left + 2.dp.toPx()).roundToInt(), (top + 8.dp.toPx()).roundToInt()),
                dstSize = IntSize(width.roundToInt().coerceAtLeast(1), height.roundToInt().coerceAtLeast(1)),
                filterQuality = FilterQuality.High,
                colorFilter = ColorFilter.tint(Color.Black.copy(alpha = 0.45f), androidx.compose.ui.graphics.BlendMode.SrcIn),
            )
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

/**
 * Зелёное кольцо при подключении (прототип: кольцо диска зеленеет + свечение).
 * Диск без камней, поэтому кольцо — полный круг; прорисовывается за 700 мс от
 * верхней точки, дальше неподвижно. Мерцания нет (запрещено владельцем).
 */
@Composable
internal fun MonogramGlow(connected: Boolean, modifier: Modifier = Modifier) {
    if (!connected) return
    val drawn = remember { Animatable(0f) }
    LaunchedEffect(Unit) { drawn.animateTo(1f, tween(RING_DRAW_MS, easing = FastOutSlowInEasing)) }
    Canvas(modifier) {
        val boxRadius = size.minDimension / 2f
        val stroke = 2.dp.toPx()
        val radius = boxRadius - stroke
        drawArc(
            color = DarkGreen,
            startAngle = -90f,
            sweepAngle = 360f * drawn.value,
            useCenter = false,
            topLeft = Offset(center.x - radius, center.y - radius),
            size = Size(radius * 2f, radius * 2f),
            style = Stroke(width = stroke),
        )
    }
}

/** Прорисовка кольца при подключении, мс. */
internal const val RING_DRAW_MS = 700

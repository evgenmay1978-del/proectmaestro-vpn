package com.maestrovpn.tv.compose.screen.tvhome

import android.content.res.Resources
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathOperation
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.premium.DarkGreen
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.roundToInt
import kotlin.math.roundToLong
import kotlin.math.sin

/**
 * Медальон-монетка как настоящий цилиндр (утверждённый тёмный дизайн 23.11.2026,
 * прототип «MaestroVPN Mobile — моё видение»).
 *
 * Почему в r708 монетка была плоской: лицо (диск + буква) лежало в одном плоском
 * слое с graphicsLayer rotationY — при повороте весь слой схлопывался в линию,
 * а «ребро» рисовалось отдельной полосой по центру за лицом и было скрыто, пока
 * лицо не станет уже полосы (угол > 85°). Буква — один плоский кадр, поэтому её
 * объём тоже пропадал на развороте. Плюс анимация дёргалась: ease-замедления на
 * концах каждого твина и перезапуски Animatable между оборотами.
 *
 * Теперь монета рисуется вручную в одном Canvas как цилиндр с настоящей толщиной:
 * лицевая и обратная грани — эллипсы, между ними золотое ребро (серп, ширина
 * ∝ |sin угла| — на развороте 90° видно широкое золотое ребро, а не линию).
 * Буква M набрана экструзией реального кадра владельца: 10 сдвинутых по глубине
 * тёмно-бронзовых копий + 2 блика фаски + лицевая грань (как слои в прототипе);
 * каждая копия лежит на своей глубине вдоль оси монеты, поэтому на развороте
 * копии расходятся по горизонтали и M показывает боковую грань/толщину.
 * На обороте та же M (буква симметрична), свет один для обеих сторон.
 *
 * Угол — один непрерывный источник на оба состояния: кадровый цикл
 * (withFrameNanos), линейные участки без ease-замедлений на концах и без
 * перезапусков анимации. Выкл — медленный поворот ±56° с паузами в анфас
 * (фазы цикла прототипа coinIdleTurn, 10 с, но линейно — по требованию
 * владельца; у прототипа был ease-in-out). Вкл — ровный оборот 3,6 с/круг.
 * Переход между состояниями — короткая линейная доводка в анфас.
 */
@Composable
internal fun MonogramMedallion(
    connected: Boolean,
    connecting: Boolean = false,
    modifier: Modifier = Modifier,
) {
    val angleState = remember { mutableFloatStateOf(0f) }
    val connectedState = rememberUpdatedState(connected)
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
    // Единственный источник угла: кадровый цикл, который никогда не
    // перезапускается (LaunchedEffect(Unit)). Угол — чистая функция времени:
    // линейные участки, паузы, без ease и без snapTo-прыжков между оборотами.
    LaunchedEffect(Unit) {
        var prevNanos = withFrameNanos { it }
        var mode = CoinMode.IDLE
        var phaseIndex = 0
        var phaseTimeMs = 0L
        var angle = 0f
        var settleFrom = 0f
        var settleTo = 0f
        var settleTimeMs = 0L
        var settleDurationMs = 1L
        while (true) {
            withFrameNanos { now ->
                var dtMs = (now - prevNanos) / 1_000_000L
                prevNanos = now
                if (dtMs < 0L) dtMs = 0L
                if (dtMs > 50L) dtMs = 50L // фон/возврат: прыжка угла не будет
                val wantConnected = connectedState.value
                if (wantConnected && mode != CoinMode.SPIN) {
                    // Подключение: продолжаем вращение с текущего угла, без рывка.
                    mode = CoinMode.SPIN
                } else if (!wantConnected && mode == CoinMode.SPIN) {
                    // Отключение: линейно довести монету в анфас (ближайшее кратное 360°).
                    val target = (angle / 360f).roundToInt() * 360f
                    settleFrom = angle
                    settleTo = target
                    val dist = abs(target - angle)
                    settleDurationMs = maxOf(1L, (dist / COIN_SETTLE_SPEED).roundToLong())
                    settleTimeMs = 0L
                    mode = CoinMode.SETTLE
                }
                when (mode) {
                    CoinMode.SPIN -> {
                        angle += COIN_SPIN_SPEED * dtMs
                        angle %= 360f
                    }
                    CoinMode.SETTLE -> {
                        settleTimeMs += dtMs
                        val f = (settleTimeMs.toFloat() / settleDurationMs).coerceIn(0f, 1f)
                        angle = settleFrom + (settleTo - settleFrom) * f
                        if (f >= 1f) {
                            angle = 0f
                            mode = CoinMode.IDLE
                            phaseIndex = 0
                            phaseTimeMs = 0L
                        }
                    }
                    CoinMode.IDLE -> {
                        var remaining = dtMs
                        while (remaining > 0L) {
                            val phase = IDLE_PHASES[phaseIndex]
                            val left = phase.durationMs - phaseTimeMs
                            val step = minOf(remaining, left)
                            phaseTimeMs += step
                            remaining -= step
                            if (phaseTimeMs >= phase.durationMs) {
                                phaseIndex = (phaseIndex + 1) % IDLE_PHASES.size
                                phaseTimeMs = 0L
                            }
                        }
                        val phase = IDLE_PHASES[phaseIndex]
                        angle = phase.from + (phase.to - phase.from) *
                            (phaseTimeMs.toFloat() / phase.durationMs)
                    }
                }
                if (angleState.floatValue != angle) angleState.floatValue = angle
            }
        }
    }
    val resources = LocalContext.current.resources
    val idle = remember(resources) { decodeFrame(resources, R.drawable.mobile_medallion_m_idle, IDLE_SAMPLE) }
    Box(modifier = modifier, contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            drawCoin(angleState.floatValue, connected, idle, pulse.value)
        }
    }
}

/** Режимы единственного источника угла. */
private enum class CoinMode { IDLE, SETTLE, SPIN }

/** Один линейный участок цикла покоя: длительность и угол от/до. */
private data class IdlePhase(val durationMs: Long, val from: Float, val to: Float)

/**
 * Цикл покоя — фазы keyframes coinIdleTurn из прототипа (10 с): пауза в анфас,
 * поворот в одну сторону, возврат в анфас, пауза, поворот в другую сторону,
 * возврат в анфас. В отличие от прототипа участки линейные (без ease-in-out) —
 * по требованию владельца: чистые движения без замедлений на концах.
 */
private val IDLE_PHASES = listOf(
    IdlePhase(1_800L, 0f, 0f),
    IdlePhase(2_000L, 0f, 56f),
    IdlePhase(1_800L, 56f, 0f),
    IdlePhase(1_200L, 0f, 0f),
    IdlePhase(1_800L, 0f, -56f),
    IdlePhase(1_400L, -56f, 0f),
)

/** Оборот монеты при подключении, мс на круг (как в утверждённом прототипе). */
private const val COIN_SPIN_PERIOD_MS = 3_600

/** Скорость вращения при подключении, град/мс (360° за 3,6 с). */
private const val COIN_SPIN_SPEED = 360f / COIN_SPIN_PERIOD_MS

/** Скорость доводки в анфас при отключении, град/мс (макс. ~400 мс на 180°). */
private const val COIN_SETTLE_SPEED = 0.45f

/** Толщина ребра монеты, доля диаметра (прототип: стопка дисков 20px на 234px). */
private const val COIN_EDGE_FRACTION = 0.09f

/** Толщина буквы M вдоль оси монеты, доля диаметра (прототип: ~11,5px на 234px). */
private const val LETTER_THICK_FRACTION = 0.05f

/** Вся монета в одном Canvas: свечение, ребро-цилиндр, лицо, экструзия буквы, вуаль. */
private fun DrawScope.drawCoin(
    angleDeg: Float,
    connected: Boolean,
    letter: ImageBitmap?,
    pulse: Float,
) {
    val s = size.minDimension
    val cx = size.width / 2f
    val cy = size.height / 2f
    val r = s / 2f
    // Угол -> косинус/синус. Обратная сторона приводится к той же геометрии:
    // буква и диск симметричны, поэтому тыл читается как та же M.
    val rad = angleDeg * (PI.toFloat() / 180f)
    var c = cos(rad)
    var sn = sin(rad)
    val back = c < 0f
    if (back) {
        c = -c
        sn = -sn
    }
    // Зелёное свечение при подключении (плоскость экрана, не вращается).
    if (connected) {
        drawCircle(
            brush = Brush.radialGradient(
                0f to DarkGreen.copy(alpha = 0.26f),
                1f to Color.Transparent,
                center = Offset(cx, cy),
                radius = s * 0.62f,
            ),
            radius = s * 0.62f,
            center = Offset(cx, cy),
        )
    }
    val thickness = s * COIN_EDGE_FRACTION
    val letterThick = s * LETTER_THICK_FRACTION
    val faceCx = cx + (thickness / 2f) * sn
    val hiddenCx = cx - (thickness / 2f) * sn
    val faceRx = r * c
    // Гнездо (socket): монета сидит в круглом отверстии; видимая стенка —
    // круг отверстия минус силуэт монеты. Рисуется под монетой (до ребра
    // и лица). При 0° отверстия нет: монета закрывает его целиком.
    if (abs(sn) > 0.001f) {
        drawSocket(cx, cy, r, faceCx, hiddenCx, faceRx, s)
    }
    // Ребро монеты: серп между скрытой и лицевой гранями на стороне, куда
    // монета повернулась (ширина ∝ |sin угла|; при 90° — широкая золотая
    // полоса, а не линия). Рисуется до лица: лицо накрывает его внутренний край.
    if (abs(sn) > 0.001f) {
        val rx = faceRx.coerceAtLeast(1f)
        val topY = cy - r
        val bottomY = cy + r
        val sweep = if (sn > 0f) -180f else 180f
        val band = Path().apply {
            moveTo(hiddenCx, topY)
            lineTo(faceCx, topY)
            arcTo(
                Rect(faceCx - rx, topY, faceCx + rx, bottomY),
                startAngleDegrees = 270f,
                sweepAngleDegrees = sweep,
                forceMoveTo = false,
            )
            lineTo(hiddenCx, bottomY)
            arcTo(
                Rect(hiddenCx - rx, topY, hiddenCx + rx, bottomY),
                startAngleDegrees = 90f,
                sweepAngleDegrees = -sweep,
                forceMoveTo = false,
            )
            close()
        }
        drawPath(
            path = band,
            brush = Brush.verticalGradient(
                0f to Color(0xFFF0CD72),
                0.45f to Color(0xFFCAA02F),
                0.8f to Color(0xFF8A5F14),
                1f to Color(0xFF6E4A0D),
                startY = topY,
                endY = bottomY,
            ),
        )
    }
    // Лицо монеты (эллипс); при ~90° лицо — линия, его не рисуем: монета — ребро.
    // Буква рисуется всегда: при ~90° её слои расходятся в боковую грань.
    val faceRect = Rect(faceCx - faceRx, cy - r, faceCx + faceRx, cy + r)
    if (faceRx >= 2f) {
        drawDiscFace(faceRect, faceCx, cy, r, connected, back)
    }
    if (letter != null) {
        drawLetter(letter, faceRect, cx, cy, s, c, sn, thickness, letterThick)
    }
    // Пульс яркости при подключении: белая вуаль поверх монеты.
    if (pulse > 0f) {
        drawCircle(
            Color.White.copy(alpha = 0.14f * pulse),
            radius = s / 2f,
            center = Offset(cx, cy),
        )
    }
}

/**
 * Стенка гнезда: угловой свет (sweep-градиент, 0° = восток, по часовой).
 * База (200, 165, 90) — то же шампань-золото, что и ребро монеты; свет
 * сверху-слева, поэтому максимум на 45° (низ-право), минимум на 225°
 * (верх-лево). Цвета — точный пересчёт формулы мокапа
 * (k = 0.75 + 0.5·(0.5 + 0.5·cos(a − 45°))) по шагам 45°; между
 * остановками линейная интерполяция вместо косинуса (отклонение ≤ ~4 ед. RGB).
 */
private val SOCKET_WALL_COLORS = listOf(
    Color(0xFFF7CE76), // 0°
    Color(0xFFFFDB7D), // 45° — самая светлая (низ-право, к свету)
    Color(0xFFF7CE76), // 90°
    Color(0xFFD2AF64), // 135°
    Color(0xFFAD9052), // 180°
    Color(0xFF9E834B), // 225° — самая тёмная (верх-лево, от света)
    Color(0xFFAD9052), // 270°
    Color(0xFFD2AF64), // 315°
    Color(0xFFF7CE76), // 360° = 0°
)

/**
 * Объёмное гнездо: круглое отверстие, в котором сидит монета.
 * Видимая стенка = круг отверстия (радиус = радиус монеты) минус силуэт
 * монеты (объединение скрытого и лицевого эллипсов). При 0° отверстия нет —
 * монета закрывает его; при повороте оно открывается с обеих сторон, и в
 * выключенном, и в подключённом состоянии. Рисуется под монетой.
 *
 * Свет — как в проверенном мокапе (~/workspace/coin-proof/render_anim.py):
 * яркое шампань-золото в гамме ребра монеты (база 200, 165, 90; самая тёмная
 * часть — всё равно среднее золото, самая светлая — светлое шампанское);
 * угловой свет сверху-слева; радиальная глубина — ярче у кромки отверстия,
 * глубже бронза к монетке (чёрный провал запрещён); яркий блик по кромке
 * сверху-слева; мягкая контактная тень вплотную к силуэту монеты.
 */
private fun DrawScope.drawSocket(
    cx: Float,
    cy: Float,
    r: Float,
    faceCx: Float,
    hiddenCx: Float,
    faceRx: Float,
    s: Float,
) {
    val rx = faceRx.coerceAtLeast(1f)
    val opening = Path().apply { addOval(Rect(cx - r, cy - r, cx + r, cy + r)) }
    val hiddenOval = Path().apply {
        addOval(Rect(hiddenCx - rx, cy - r, hiddenCx + rx, cy + r))
    }
    val faceOval = Path().apply {
        addOval(Rect(faceCx - rx, cy - r, faceCx + rx, cy + r))
    }
    val silhouette = Path.combine(PathOperation.Union, hiddenOval, faceOval)
    val hole = Path.combine(PathOperation.Difference, opening, silhouette)
    // Масштаб мокапа: у него диаметр монеты 432px, в приложении — s.
    val pxScale = s / 432f
    clipPath(hole) {
        // Стенка: угловой свет (sweep-градиент, центр — центр монеты).
        drawCircle(
            brush = Brush.sweepGradient(SOCKET_WALL_COLORS, center = Offset(cx, cy)),
            radius = r,
            center = Offset(cx, cy),
        )
        // Радиальная глубина: ярче у кромки, глубже бронза к монетке
        // (чёрная вуаль 0.55 в центре → прозрачно у кромки, как в мокапе).
        drawCircle(
            brush = Brush.radialGradient(
                0f to Color.Black.copy(alpha = 0.55f),
                1f to Color.Transparent,
                center = Offset(cx, cy),
                radius = r,
            ),
            radius = r,
            center = Offset(cx, cy),
        )
        // Блик по кромке отверстия сверху-слева + тёмная кромка снизу-справа.
        val rimInset = 2f * pxScale
        val rimTopLeft = Offset(cx - r + rimInset, cy - r + rimInset)
        val rimSize = Size(2f * (r - rimInset), 2f * (r - rimInset))
        drawArc(
            color = Color(0xDCFAE196),
            startAngle = 150f,
            sweepAngle = 140f,
            useCenter = false,
            topLeft = rimTopLeft,
            size = rimSize,
            style = Stroke(width = 3f * pxScale),
        )
        drawArc(
            color = Color.Black.copy(alpha = 0.55f),
            startAngle = 330f,
            sweepAngle = 140f,
            useCenter = false,
            topLeft = rimTopLeft,
            size = rimSize,
            style = Stroke(width = 3f * pxScale),
        )
        // Контактная тень: мягкая тёмная полоса вплотную к силуэту монеты.
        // В Canvas нет размытия произвольного контура, поэтому вместо
        // гауссова пятна мокапа — два вложенных штриха по контуру обоих
        // эллипсов силуэта (широкий бледный + узкий плотный); внутренняя
        // половина штрихов обрезана регионом отверстия, наружу видна
        // только мягкая полоса (пик ~0.25 у кромки монеты, как в мокапе).
        drawOval(
            color = Color.Black.copy(alpha = 0.12f),
            topLeft = Offset(hiddenCx - rx, cy - r),
            size = Size(rx * 2f, r * 2f),
            style = Stroke(width = 0.05f * s),
        )
        drawOval(
            color = Color.Black.copy(alpha = 0.12f),
            topLeft = Offset(faceCx - rx, cy - r),
            size = Size(rx * 2f, r * 2f),
            style = Stroke(width = 0.05f * s),
        )
        drawOval(
            color = Color.Black.copy(alpha = 0.15f),
            topLeft = Offset(hiddenCx - rx, cy - r),
            size = Size(rx * 2f, r * 2f),
            style = Stroke(width = 0.02f * s),
        )
        drawOval(
            color = Color.Black.copy(alpha = 0.15f),
            topLeft = Offset(faceCx - rx, cy - r),
            size = Size(rx * 2f, r * 2f),
            style = Stroke(width = 0.02f * s),
        )
    }
}

/** Лицо диска по прототипу: тёмный радиальный диск, блик сверху, тонкие золотые кольца. */
private fun DrawScope.drawDiscFace(
    faceRect: Rect,
    faceCx: Float,
    cy: Float,
    r: Float,
    connected: Boolean,
    back: Boolean,
) {
    val faceOval = Path().apply { addOval(faceRect) }
    clipPath(faceOval) {
        // Радиальный диск; тыл темнее — как coin-back в прототипе.
        val (c0, c1, c2) = if (back) {
            Triple(Color(0xFF1D1509), Color(0xFF120D05), Color(0xFF090604))
        } else {
            Triple(Color(0xFF251C10), Color(0xFF171208), Color(0xFF0C0905))
        }
        drawCircle(
            brush = Brush.radialGradient(
                0f to c0,
                0.58f to c1,
                1f to c2,
                center = Offset(faceCx, cy),
                radius = r,
            ),
            radius = r,
            center = Offset(faceCx, cy),
        )
        // Верхний блик (свет один для обеих сторон — сверху по центру сцены).
        val highlight = if (back) Color(0x1AF6DA8A) else Color(0x2BF6DA8A)
        drawCircle(
            brush = Brush.radialGradient(
                0f to highlight,
                1f to Color.Transparent,
                center = Offset(faceCx, cy - r * 0.55f),
                radius = r * 0.9f,
            ),
            radius = r,
            center = Offset(faceCx, cy),
        )
        // Внутренняя тень сверху/снизу.
        drawRect(
            brush = Brush.verticalGradient(
                0f to Color(0x80000000),
                0.25f to Color.Transparent,
                0.75f to Color.Transparent,
                1f to Color(0xA6000000),
                startY = faceRect.top,
                endY = faceRect.bottom,
            ),
        )
    }
    // Кольца ободка (в прототипе — inset box-shadow): внешнее золотое (зелёное при
    // подключении), тёмный зазор, тонкая золотая волосяная линия, кромка по самому краю.
    val ring = if (connected) DarkGreen else Color(0xFFC9992E)
    val ringHair = if (connected) DarkGreen.copy(alpha = 0.42f) else Color(0x73D9A441)
    val ringRim = if (connected) DarkGreen.copy(alpha = 0.30f) else Color(0x42D9A441)
    ringOval(faceRect, 0.75.dp.toPx(), ring, 1.5.dp.toPx())
    ringOval(faceRect, 2.dp.toPx(), Color(0xEB120C03), 1.dp.toPx())
    ringOval(faceRect, 2.5.dp.toPx(), ringHair, 0.75.dp.toPx())
    ringOval(faceRect, 0.25.dp.toPx(), ringRim, 0.5.dp.toPx())
}

/** Одно кольцо ободка как эллипс, вписанный в лицо с отступом. */
private fun DrawScope.ringOval(faceRect: Rect, inset: Float, color: Color, strokeWidth: Float) {
    val rx = faceRect.width / 2f - inset
    val ry = faceRect.height / 2f - inset
    if (rx <= 0.5f || ry <= 0.5f) return
    drawOval(
        color = color,
        topLeft = Offset(faceRect.center.x - rx, faceRect.center.y - ry),
        size = Size(rx * 2f, ry * 2f),
        style = Stroke(width = strokeWidth),
    )
}

// Посадка буквы по прототипу: кадр 73,02% ширины сцены, верх 22,29%, левый край 12,78%,
// высота кадра 51,73% (768×544). Центр чернил буквы при этом — точно в центре монеты.
private const val FRAME_LEFT = 0.1278f
private const val FRAME_TOP = 0.2229f
private const val FRAME_WIDTH = 0.7302f
private const val FRAME_HEIGHT = 0.5173f
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

/**
 * Экструзия буквы M из реального кадра владельца (как слои в прототипе).
 *
 * Каждый слой лежит на своей глубине z вдоль оси монеты (от грани диска наружу)
 * и проецируется как x' = cx + (x − cx)·cos θ + z·sin θ. В анфас (θ = 0) все
 * слои совпадают, и видна статичная 2D-экструзия прототипа: 10 тёмно-бронзовых
 * копий, сдвинутых вниз-вправо с шагом 0,72px/0,9px (на монете 234px), плюс
 * 2 бледных блика фаски вверх-влево. На развороте слои расходятся по
 * горизонтали (сдвиг ∝ sin θ) — M показывает боковую грань/толщину, а не
 * схлопывается в линию. Тень буквы лежит на грани диска (z = T/2).
 *
 * Буква не обрезается по диску: на крутых углах её боковая грань физически
 * выступает за кромку диска — так и должно быть.
 */
private fun DrawScope.drawLetter(
    bitmap: ImageBitmap,
    faceRect: Rect,
    cx: Float,
    cy: Float,
    s: Float,
    c: Float,
    sn: Float,
    thickness: Float,
    letterThick: Float,
) {
    val left = size.width * FRAME_LEFT
    val top = size.height * FRAME_TOP
    val width = size.width * FRAME_WIDTH
    val height = size.height * FRAME_HEIGHT
    // Тень буквы на диске (два прохода — мягче, как drop-shadow прототипа).
    // При ~90° лицо — линия, тень не рисуем.
    if (faceRect.width >= 4f) {
        val faceOval = Path().apply { addOval(faceRect) }
        clipPath(faceOval) {
            drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, thickness / 2f, 4.dp.toPx(), 14.dp.toPx(), tint = Color.Black.copy(alpha = 0.30f), quality = FilterQuality.Medium, tintBlend = BlendMode.SrcIn)
            drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, thickness / 2f, 2.dp.toPx(), 8.dp.toPx(), tint = Color.Black.copy(alpha = 0.45f), quality = FilterQuality.Medium, tintBlend = BlendMode.SrcIn)
        }
    }
    // 10 тёмно-бронзовых копий: самая глубокая — самая сдвинутая и тёмная.
    val pxScale = s / 234f
    for (d in 0..9) {
        val depth = d / 9f
        val ox = (9 - d) * 0.72f * pxScale
        val oy = (9 - d) * 0.9f * pxScale
        val z = thickness / 2f + depth * letterThick
        val b = 0.24f + depth * 0.42f
        val bronze = Color(red = b, green = b * 0.82f, blue = b * 0.58f)
        drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, z, ox, oy, tint = bronze, quality = FilterQuality.Medium, tintBlend = BlendMode.Modulate)
    }
    // 2 бледных блика фаски вверх-влево.
    drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, thickness / 2f + letterThick * 0.97f, -0.55f * pxScale, -0.7f * pxScale, tint = Color(0xFFF6DA8A).copy(alpha = 0.22f), quality = FilterQuality.Medium, tintBlend = BlendMode.SrcIn)
    drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, thickness / 2f + letterThick * 0.97f, -1.0f * pxScale, -1.25f * pxScale, tint = Color(0xFFF6DA8A).copy(alpha = 0.38f), quality = FilterQuality.Medium, tintBlend = BlendMode.SrcIn)
    // Лицевая грань: сам кадр, без изменений.
    drawLetterCopy(bitmap, left, top, width, height, cx, c, sn, thickness / 2f + letterThick, 0f, 0f, tint = null, quality = FilterQuality.High)
}

/**
 * Одна копия кадра буквы, спроецированная на экран при повороте монеты.
 * [tint] = null — кадр как есть; иначе цветофильтр [tintBlend] по кадру:
 * Modulate — умножение (тёмная бронза боковых граней), SrcIn — заливка по
 * альфе (чёрная тень, бледное золото бликов). На канвас копия всегда
 * кладётся обычным SrcOver. Сдвиг ox/oy — в плоскости грани (масштабируется
 * cos θ), z — глубина вдоль оси монеты (сдвиг ∝ sin θ).
 */
private fun DrawScope.drawLetterCopy(
    bitmap: ImageBitmap,
    left: Float,
    top: Float,
    width: Float,
    height: Float,
    cx: Float,
    c: Float,
    sn: Float,
    z: Float,
    ox: Float,
    oy: Float,
    tint: Color?,
    quality: FilterQuality,
    tintBlend: BlendMode = BlendMode.SrcOver,
) {
    val dstLeft = cx + (left + ox - cx) * c + z * sn
    val dstWidth = (width * c).coerceAtLeast(1f)
    drawImage(
        image = bitmap,
        srcOffset = IntOffset.Zero,
        srcSize = IntSize(bitmap.width, bitmap.height),
        dstOffset = IntOffset(dstLeft.roundToInt(), (top + oy).roundToInt()),
        dstSize = IntSize(dstWidth.roundToInt().coerceAtLeast(1), height.roundToInt().coerceAtLeast(1)),
        filterQuality = quality,
        colorFilter = tint?.let { ColorFilter.tint(it, tintBlend) },
    )
}

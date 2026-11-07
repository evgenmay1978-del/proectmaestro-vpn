package com.maestrovpn.tv.compose.screen.tvhome

import android.content.res.Resources
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlin.math.ceil
import kotlin.math.roundToInt

/**
 * Объёмная буква «M» отдельным слоем (05.10.2026). Буква больше не впечатана в диск медальона:
 * в покое рисуется статичный кадр владельца, при подключении — 72 кадра вращения вокруг
 * вертикальной оси (шаг 5°, линейно, полный оборот 3,6 с).
 *
 * Вращение — заранее отрендеренная экструзия силуэта: у буквы видна толщина и грани, как у
 * настоящей металлической монограммы. Кадры лежат в res/drawable-nodpi как WebP с альфой.
 *
 * Окно кадра — 768×544 в системе ассета 1024×1024 с левым верхним углом (128, 280): те же
 * пропорции, что у диска медальона, поэтому буква встаёт на прежнее место без подгонки
 * (размер и позиция медальона не меняются).
 *
 * 07.11.2026:
 *  - статичный кадр декодируется в полном разрешении (раньше — вдвое меньше и растягивался
 *    в 1,3–1,4 раза на телефоне, буква в покое была мыльной); кадры вращения остаются ×½;
 *  - нет рывков на старте и остановке: статичный кадр и кадры вращения — разные рендеры,
 *    поэтому между ними плавный переход ([FADE_MS]); при отключении буква докручивается до
 *    ближайшего фронтального кадра (0° или 180°, кадр 036 ≈ анфас) и только потом гаснет в статику;
 *  - 72 кадра (~30 МБ) грузятся при первом подключении, а не при каждом запуске;
 *  - текущий кадр читается в фазе отрисовки — без рекомпозиции на каждом кадре.
 */
private const val SPIN_PERIOD_MS = 3_600L
private const val SPIN_FRAME_COUNT = 72
private const val SPIN_SAMPLE = 2
private const val IDLE_SAMPLE = 1
private const val FADE_MS = 250
private val FRAMES_PER_MS = SPIN_FRAME_COUNT.toFloat() / SPIN_PERIOD_MS
/** Фронтальные кадры: 0 и 36 (0° и 180°). */
private const val FRONT_STEP = SPIN_FRAME_COUNT / 2f

// В кадрах буква шире диска: её нижние засечки расходятся до r=402 при радиусе диска 372
// (система ассета 1024), поэтому на голом дереве они вылезали за золотое кольцо и «вращались»
// вместе с буквой. Уменьшаем монограмму так, чтобы она целиком встала в кольцо (402*0.86 = 346),
// а клип по 360/1024 оставляем страховкой: ничего за диском оказаться не может.
private const val FRAME_SCALE = 0.86f
private const val FRAME_CLIP_RADIUS = 360f / 1024f

// окно кадра в долях бокса медальона: 128/1024, 280/1024, 768/1024, 544/1024
private const val FRAME_LEFT = 0.125f
private const val FRAME_TOP = 0.2734375f
private const val FRAME_WIDTH = 0.75f
private const val FRAME_HEIGHT = 0.53125f

private val SPIN_FRAME_IDS = intArrayOf(
    R.drawable.mobile_medallion_m_spin_000,
    R.drawable.mobile_medallion_m_spin_001,
    R.drawable.mobile_medallion_m_spin_002,
    R.drawable.mobile_medallion_m_spin_003,
    R.drawable.mobile_medallion_m_spin_004,
    R.drawable.mobile_medallion_m_spin_005,
    R.drawable.mobile_medallion_m_spin_006,
    R.drawable.mobile_medallion_m_spin_007,
    R.drawable.mobile_medallion_m_spin_008,
    R.drawable.mobile_medallion_m_spin_009,
    R.drawable.mobile_medallion_m_spin_010,
    R.drawable.mobile_medallion_m_spin_011,
    R.drawable.mobile_medallion_m_spin_012,
    R.drawable.mobile_medallion_m_spin_013,
    R.drawable.mobile_medallion_m_spin_014,
    R.drawable.mobile_medallion_m_spin_015,
    R.drawable.mobile_medallion_m_spin_016,
    R.drawable.mobile_medallion_m_spin_017,
    R.drawable.mobile_medallion_m_spin_018,
    R.drawable.mobile_medallion_m_spin_019,
    R.drawable.mobile_medallion_m_spin_020,
    R.drawable.mobile_medallion_m_spin_021,
    R.drawable.mobile_medallion_m_spin_022,
    R.drawable.mobile_medallion_m_spin_023,
    R.drawable.mobile_medallion_m_spin_024,
    R.drawable.mobile_medallion_m_spin_025,
    R.drawable.mobile_medallion_m_spin_026,
    R.drawable.mobile_medallion_m_spin_027,
    R.drawable.mobile_medallion_m_spin_028,
    R.drawable.mobile_medallion_m_spin_029,
    R.drawable.mobile_medallion_m_spin_030,
    R.drawable.mobile_medallion_m_spin_031,
    R.drawable.mobile_medallion_m_spin_032,
    R.drawable.mobile_medallion_m_spin_033,
    R.drawable.mobile_medallion_m_spin_034,
    R.drawable.mobile_medallion_m_spin_035,
    R.drawable.mobile_medallion_m_spin_036,
    R.drawable.mobile_medallion_m_spin_037,
    R.drawable.mobile_medallion_m_spin_038,
    R.drawable.mobile_medallion_m_spin_039,
    R.drawable.mobile_medallion_m_spin_040,
    R.drawable.mobile_medallion_m_spin_041,
    R.drawable.mobile_medallion_m_spin_042,
    R.drawable.mobile_medallion_m_spin_043,
    R.drawable.mobile_medallion_m_spin_044,
    R.drawable.mobile_medallion_m_spin_045,
    R.drawable.mobile_medallion_m_spin_046,
    R.drawable.mobile_medallion_m_spin_047,
    R.drawable.mobile_medallion_m_spin_048,
    R.drawable.mobile_medallion_m_spin_049,
    R.drawable.mobile_medallion_m_spin_050,
    R.drawable.mobile_medallion_m_spin_051,
    R.drawable.mobile_medallion_m_spin_052,
    R.drawable.mobile_medallion_m_spin_053,
    R.drawable.mobile_medallion_m_spin_054,
    R.drawable.mobile_medallion_m_spin_055,
    R.drawable.mobile_medallion_m_spin_056,
    R.drawable.mobile_medallion_m_spin_057,
    R.drawable.mobile_medallion_m_spin_058,
    R.drawable.mobile_medallion_m_spin_059,
    R.drawable.mobile_medallion_m_spin_060,
    R.drawable.mobile_medallion_m_spin_061,
    R.drawable.mobile_medallion_m_spin_062,
    R.drawable.mobile_medallion_m_spin_063,
    R.drawable.mobile_medallion_m_spin_064,
    R.drawable.mobile_medallion_m_spin_065,
    R.drawable.mobile_medallion_m_spin_066,
    R.drawable.mobile_medallion_m_spin_067,
    R.drawable.mobile_medallion_m_spin_068,
    R.drawable.mobile_medallion_m_spin_069,
    R.drawable.mobile_medallion_m_spin_070,
    R.drawable.mobile_medallion_m_spin_071,
)

/**
 * Куда докрутить букву при отключении: ближайший фронтальный кадр (0° или 180°) впереди по ходу
 * вращения, в незавёрнутых кадрах (72 = следующий полный оборот, т.е. кадр 0). Не дальше
 * пол-оборота: [FRONT_STEP] кадров = 1,8 с.
 */
internal fun spinStopTarget(position: Float): Float = ceil(position / FRONT_STEP) * FRONT_STEP

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

@Composable
internal fun MonogramSpin(connected: Boolean, modifier: Modifier = Modifier) {
    val resources = LocalContext.current.resources
    val idle = remember(resources) { decodeFrame(resources, R.drawable.mobile_medallion_m_idle, IDLE_SAMPLE) }
    // Все 72 кадра грузим один раз (в 2 раза уменьшенные — ~30 МБ), но только при первом подключении.
    var frames by remember(resources) { mutableStateOf<Array<ImageBitmap?>?>(null) }
    // Положение оборота в кадрах [0, 72) и доля статичного кадра (1 — только статика).
    val phase = remember { mutableFloatStateOf(0f) }
    val idleMix = remember { Animatable(1f) }

    LaunchedEffect(connected, resources) {
        if (connected) {
            if (frames == null) {
                frames = withContext(Dispatchers.IO) {
                    Array(SPIN_FRAME_COUNT) { index -> decodeFrame(resources, SPIN_FRAME_IDS[index], SPIN_SAMPLE) }
                }
            }
            launch { idleMix.animateTo(0f, tween(FADE_MS, easing = FastOutSlowInEasing)) }
            var last = withFrameNanos { it }
            while (true) {
                withFrameNanos { now ->
                    phase.floatValue = (phase.floatValue + (now - last) / 1_000_000f * FRAMES_PER_MS) % SPIN_FRAME_COUNT
                    last = now
                }
            }
        } else {
            // Докрутить до ближайшего фронтального кадра впереди, затем плавно уйти в статику.
            var position = phase.floatValue
            val target = spinStopTarget(position)
            if (idleMix.value < 1f && target > position) {
                var last = withFrameNanos { it }
                while (position < target) {
                    withFrameNanos { now ->
                        position = minOf(target, position + (now - last) / 1_000_000f * FRAMES_PER_MS)
                        last = now
                    }
                    phase.floatValue = position % SPIN_FRAME_COUNT
                }
            }
            phase.floatValue = target % SPIN_FRAME_COUNT
            idleMix.animateTo(1f, tween(FADE_MS, easing = FastOutSlowInEasing))
            phase.floatValue = 0f
        }
    }

    // Offscreen-слой: переход «кадр вращения ↔ статика» складывается внутри слоя (Plus), поэтому
    // буква не просвечивает посередине перехода.
    Canvas(modifier.graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }) {
        val mix = idleMix.value
        val spin = frames?.getOrNull(phase.floatValue.toInt() % SPIN_FRAME_COUNT)
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
        val dstOffset = IntOffset(left.roundToInt(), top.roundToInt())
        val dstSize = IntSize(width.roundToInt().coerceAtLeast(1), height.roundToInt().coerceAtLeast(1))
        clipPath(disc) {
            when {
                spin == null || mix >= 0.999f -> idle?.let { drawFrame(it, dstOffset, dstSize, 1f, BlendMode.SrcOver) }
                mix <= 0.001f || idle == null -> drawFrame(spin, dstOffset, dstSize, 1f, BlendMode.SrcOver)
                else -> {
                    drawFrame(spin, dstOffset, dstSize, 1f - mix, BlendMode.SrcOver)
                    drawFrame(idle, dstOffset, dstSize, mix, BlendMode.Plus)
                }
            }
        }
    }
}

private fun DrawScope.drawFrame(
    image: ImageBitmap,
    dstOffset: IntOffset,
    dstSize: IntSize,
    alpha: Float,
    blendMode: BlendMode,
) {
    drawImage(
        image = image,
        srcOffset = IntOffset.Zero,
        srcSize = IntSize(image.width, image.height),
        dstOffset = dstOffset,
        dstSize = dstSize,
        alpha = alpha,
        blendMode = blendMode,
        filterQuality = FilterQuality.High,
    )
}

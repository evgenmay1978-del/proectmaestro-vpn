package com.maestrovpn.tv.compose.screen.tvhome

import android.content.res.Resources
import android.graphics.BitmapFactory
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
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
 */
private const val SPIN_PERIOD_MS = 3_600L
private const val SPIN_FRAME_COUNT = 72
private const val SPIN_CACHE_FRAMES = 16
private const val SPIN_PREFETCH_FRAMES = 8

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

/** LRU-кэш декодированных кадров: держим окно вокруг текущего, а не все 72 сразу (это ~120 МБ). */
private class SpinFrameCache(private val resources: Resources) {
    private val bitmaps = object : LinkedHashMap<Int, ImageBitmap>(32, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, ImageBitmap>?): Boolean =
            size > SPIN_CACHE_FRAMES
    }

    fun peek(index: Int): ImageBitmap? = bitmaps[index]

    fun load(index: Int): ImageBitmap? {
        bitmaps[index]?.let { return it }
        val bitmap = runCatching {
            BitmapFactory.decodeResource(
                resources,
                SPIN_FRAME_IDS[index],
                BitmapFactory.Options().apply { inPreferredConfig = Bitmap.Config.ARGB_8888 },
            )
        }.getOrNull() ?: return null
        val image = bitmap.asImageBitmap()
        bitmaps[index] = image
        return image
    }
}

@Composable
private fun rememberIdleFrame(resources: Resources): ImageBitmap? = remember(resources) {
    runCatching {
        BitmapFactory.decodeResource(
            resources,
            R.drawable.mobile_medallion_m_idle,
            BitmapFactory.Options().apply { inPreferredConfig = Bitmap.Config.ARGB_8888 },
        ).asImageBitmap()
    }.getOrNull()
}

@Composable
internal fun MonogramSpin(connected: Boolean, modifier: Modifier = Modifier) {
    val resources = LocalContext.current.resources
    val cache = remember(resources) { SpinFrameCache(resources) }
    val idle = rememberIdleFrame(resources)
    var frameIndex by remember { mutableIntStateOf(0) }
    var loaded by remember { mutableIntStateOf(0) }

    LaunchedEffect(connected) {
        if (!connected) {
            frameIndex = 0
            return@LaunchedEffect
        }
        val start = withFrameNanos { it }
        while (true) {
            withFrameNanos { now ->
                val elapsed = (now - start) / 1_000_000L
                frameIndex = ((elapsed % SPIN_PERIOD_MS) * SPIN_FRAME_COUNT / SPIN_PERIOD_MS).toInt()
            }
        }
    }

    LaunchedEffect(connected, frameIndex) {
        if (!connected) return@LaunchedEffect
        withContext(Dispatchers.IO) {
            for (offset in 0 until SPIN_PREFETCH_FRAMES) {
                cache.load((frameIndex + offset) % SPIN_FRAME_COUNT)
            }
        }
        loaded++
    }

    val revision = loaded
    val frame = remember(connected, frameIndex, revision) {
        if (!connected) idle else cache.peek(frameIndex) ?: cache.peek(0) ?: idle
    }

    Canvas(modifier) {
        val image = frame ?: return@Canvas
        val left = size.width * FRAME_LEFT
        val top = size.height * FRAME_TOP
        val width = size.width * FRAME_WIDTH
        val height = size.height * FRAME_HEIGHT
        drawImage(
            image = image,
            srcOffset = IntOffset.Zero,
            srcSize = IntSize(image.width, image.height),
            dstOffset = IntOffset(left.roundToInt(), top.roundToInt()),
            dstSize = IntSize(
                width.roundToInt().coerceAtLeast(1),
                height.roundToInt().coerceAtLeast(1),
            ),
            filterQuality = FilterQuality.High,
        )
    }
}

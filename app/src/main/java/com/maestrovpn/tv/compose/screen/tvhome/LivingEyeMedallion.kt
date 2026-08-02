package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationVector1D
import androidx.compose.animation.core.FastOutLinearInEasing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.exp
import kotlin.math.ln
import kotlin.math.roundToInt
import kotlin.math.roundToLong
import kotlin.math.sqrt
import kotlin.random.Random

/**
 * Phone-only living version of the owner's eye.
 *
 * The carved plaque and medallion live in the fixed phone background and never animate.
 * Only the eye aperture is layered here:
 *
 *  * sclera stays fixed;
 *  * iris and pupil perform short saccades together;
 *  * the pupil changes radius inside a fixed outer iris;
 *  * the corneal catchlight follows only 8% of gaze translation;
 *  * every blink passes through the supplied squint frame.
 *
 * Source measurements and reconstruction limits are recorded in
 * `docs/design/mobile-eye-natural/asset_metadata.json`.
 */
@Composable
internal fun LivingEyeMedallion(
    connected: Boolean,
    touchGaze: Offset? = null,
    opennessOverride: Float? = null,
    modifier: Modifier = Modifier,
) {
    val openState = ImageBitmap.imageResource(R.drawable.mobile_eye_open)
    val squintState = ImageBitmap.imageResource(R.drawable.mobile_eye_squint)
    val closedState = ImageBitmap.imageResource(R.drawable.mobile_eye_closed)
    val sclera = ImageBitmap.imageResource(R.drawable.mobile_eye_sclera)
    val iris = ImageBitmap.imageResource(R.drawable.mobile_eye_iris)
    val catchlight = ImageBitmap.imageResource(R.drawable.mobile_eye_catchlight)

    // 0 = open, 0.5 = the owner's squint frame, 1 = the owner's closed frame.
    val lidPhase = remember { Animatable(1f) }
    val blinkEyeShift = remember { Animatable(0f) }
    val gazeX = remember { Animatable(0f) } // source-frame pixels
    val gazeY = remember { Animatable(0f) }
    val pupilScale = remember { Animatable(PUPIL_DARK_SCALE) }
    val glow = remember { Animatable(0f) }

    val blinkRandom = remember { Random(System.nanoTime().toInt()) }
    val gazeRandom = remember { Random(System.nanoTime().toInt() xor 0x4D414553) }
    val pupilRandom = remember { Random(System.nanoTime().toInt() xor 0x54524F56) }

    // Connection transitions and autonomous blinking share one lid clock, so they cannot race.
    LaunchedEffect(connected, opennessOverride) {
        blinkEyeShift.snapTo(0f)
        when {
            opennessOverride != null -> {
                lidPhase.snapTo(1f - opennessOverride.coerceIn(0f, 1f))
                return@LaunchedEffect
            }

            !connected -> {
                closeForDisconnect(lidPhase)
                return@LaunchedEffect
            }

            else -> openForConnection(lidPhase)
        }

        while (isActive) {
            delay(blinkRandom.nextBlinkDelayMillis())
            blinkOnce(lidPhase, blinkEyeShift)

            // Real double blinks occur, but should remain an occasional surprise.
            if (blinkRandom.nextFloat() < 0.10f) {
                delay(blinkRandom.nextLong(140L, 221L))
                blinkOnce(lidPhase, blinkEyeShift)
            }
        }
    }

    // A fixation is still; movement between fixations is a fast saccade, not smooth roaming.
    LaunchedEffect(connected, touchGaze, opennessOverride) {
        if (!connected || opennessOverride != null) {
            gazeTo(gazeX, gazeY, 0f, 0f, durationMillis = 45)
            return@LaunchedEffect
        }

        touchGaze?.let { target ->
            gazeTo(
                gazeX = gazeX,
                gazeY = gazeY,
                targetX = target.x.coerceIn(-1f, 1f) * MAX_GAZE_X,
                targetY = target.y.coerceIn(-1f, 1f) * MAX_GAZE_Y,
                durationMillis = 44,
            )
            return@LaunchedEffect
        }

        while (isActive) {
            delay(gazeRandom.nextLong(800L, 3_501L))

            val centreBias = if (gazeRandom.nextFloat() < 0.34f) 0.28f else 1f
            val targetX = gazeRandom.nextFloat(-MAX_GAZE_X, MAX_GAZE_X) * centreBias
            val targetY = gazeRandom.nextFloat(-MAX_GAZE_Y, MAX_GAZE_Y) * centreBias
            gazeTo(gazeX, gazeY, targetX, targetY, durationMillis = 42)

            // A rare sub-pixel microsaccade is visible at 60 Hz as one restrained step.
            if (gazeRandom.nextFloat() < 0.28f) {
                delay(gazeRandom.nextLong(180L, 521L))
                gazeTo(
                    gazeX,
                    gazeY,
                    (gazeX.value + gazeRandom.nextFloat(-0.7f, 0.7f))
                        .coerceIn(-MAX_GAZE_X, MAX_GAZE_X),
                    (gazeY.value + gazeRandom.nextFloat(-0.35f, 0.35f))
                        .coerceIn(-MAX_GAZE_Y, MAX_GAZE_Y),
                    durationMillis = 18,
                )
            }
        }
    }

    // Opening exposes the eye to light: latency, quick constriction, then slower redilation.
    // Idle "hippus" is irregular and small rather than a mechanical sine wave.
    LaunchedEffect(connected, opennessOverride) {
        if (!connected || opennessOverride != null) {
            pupilScale.snapTo(PUPIL_DARK_SCALE)
            return@LaunchedEffect
        }

        pupilScale.snapTo(PUPIL_DARK_SCALE)
        delay(230L)
        pupilScale.animateTo(
            PUPIL_BRIGHT_SCALE,
            tween(durationMillis = 420, easing = FastOutSlowInEasing),
        )
        pupilScale.animateTo(
            1f,
            tween(durationMillis = 980, easing = LinearOutSlowInEasing),
        )

        while (isActive) {
            delay(pupilRandom.nextLong(1_200L, 2_801L))
            pupilScale.animateTo(
                pupilRandom.nextFloat(0.975f, 1.026f),
                tween(
                    durationMillis = pupilRandom.nextInt(620, 1_101),
                    easing = FastOutSlowInEasing,
                ),
            )
        }
    }

    // Свет следует состоянию туннеля, а не таймеру: гаснет на отключении, РАЗГОРАЕТСЯ пока идёт
    // подключение (с лёгким пульсом, чтобы читалось «работает, а не завис»), и держится ровно,
    // когда связь есть.
    //
    // ⛔ На ветке `feat/mobile-4d-redesign` этот блок читал отдельный `EyeState`, но тот коммит
    // правит `SFANavigation.kt` и `TvHomeScreen.kt` — файлы под нулевым TV-диффом. Здесь тот же
    // сигнал уже приходит через `opennessOverride` (0f / 0.5f / null), который выставляет
    // телефонный `Mobile4DHome`. Вид тот же, ТВ не задет.
    LaunchedEffect(connected, opennessOverride) {
        when {
            opennessOverride != null && opennessOverride <= 0.01f ->
                glow.animateTo(0f, tween(durationMillis = 220))

            opennessOverride != null -> {
                glow.animateTo(
                    GLOW_CONNECTING_MIN,
                    tween(durationMillis = 420, easing = LinearOutSlowInEasing),
                )
                while (true) {
                    glow.animateTo(GLOW_CONNECTING_MAX, tween(durationMillis = 780, easing = LinearEasing))
                    glow.animateTo(GLOW_CONNECTING_MIN, tween(durationMillis = 780, easing = LinearEasing))
                }
            }

            connected -> glow.animateTo(
                GLOW_CONNECTED,
                tween(durationMillis = 620, easing = LinearOutSlowInEasing),
            )

            else -> glow.animateTo(0f, tween(durationMillis = 220))
        }
    }

    Canvas(modifier = modifier) {
        // Клип по бронзовому кольцу. Заменил прежнее ужимание всего слоя: зелень, что выходит
        // за кольцо, теперь прячется ПОД него, а радужка, зрачок и блик сохраняют исходный
        // размер. Клип охватывает ВСЕ состояния (открыт/прищур/закрыт) — иначе подрезанным
        // оказался бы только один кадр и моргание «прыгало» бы по границе.
        val bronzeInset = livingEyeBronzeInset(size.width, size.height)
        val medallion = minOf(size.width, size.height)
        val bronzeClip = Path().apply {
            addOval(
                Rect(
                    left = (size.width - medallion) / 2f + bronzeInset,
                    top = (size.height - medallion) / 2f + bronzeInset,
                    right = (size.width + medallion) / 2f - bronzeInset,
                    bottom = (size.height + medallion) / 2f - bronzeInset,
                ),
            )
        }
        clipPath(bronzeClip) {
            val layerFit = fitLivingEyeLayer(width = size.width, height = size.height)
            val phase = lidPhase.value.coerceIn(0f, 1f)
            val openToSquint = (phase / 0.5f).coerceIn(0f, 1f)
            val squintToClosed = ((phase - 0.5f) / 0.5f).coerceIn(0f, 1f)

            // Основа — открытый кадр владельца. Маппинг снова прямой (virtualScale), то есть слой
            // опять сведён с глазом так же, как до PR #72. ⛔ Самого `mobile_home_scene.webp`
            // больше нет (удалён в c15b4e3): Home собирается из 4D-атласа.
            // ⛔ Комментарий, стоявший здесь до 31.07.2026, утверждал обратное («слой БОЛЬШЕ не
            // совпадает с фоном, и это намеренно») и описывал промежуточное решение, при котором
            // весь слой ужимался в кольцо. Владелец его отверг: глаз терял треть площади. Зелень
            // с кольца убирает клип выше, а не масштаб.
            drawSourceLayer(
                image = openState,
                sourceX = LIVING_EYE_STATE_X,
                sourceY = LIVING_EYE_STATE_Y,
                sourceWidth = LIVING_EYE_STATE_WIDTH,
                sourceHeight = LIVING_EYE_STATE_HEIGHT,
                layerFit = layerFit,
            )

            if (phase <= 0.5f) {
                val aperture = eyeAperturePath(layerFit)
                clipPath(aperture) {
                    drawSourceLayer(
                        image = sclera,
                        sourceX = SCLERA_X,
                        sourceY = SCLERA_Y,
                        sourceWidth = SCLERA_WIDTH,
                        sourceHeight = SCLERA_HEIGHT,
                        layerFit = layerFit,
                    )

                    // During a routine blink the globe moves a trace down and medially.
                    val irisX = gazeX.value - BLINK_NASAL_SHIFT * blinkEyeShift.value
                    val irisY = gazeY.value + BLINK_DOWN_SHIFT * blinkEyeShift.value
                    drawSourceLayer(
                        image = iris,
                        sourceX = IRIS_X + irisX,
                        sourceY = IRIS_Y + irisY,
                        sourceWidth = IRIS_SIZE,
                        sourceHeight = IRIS_SIZE,
                        layerFit = layerFit,
                    )

                    val pupilCenter = sourcePoint(
                        layerFit = layerFit,
                        x = PUPIL_CENTER_X + irisX,
                        y = PUPIL_CENTER_Y + irisY,
                    )
                    val pupilRadius = layerFit.mapSourceLength(
                        PUPIL_NEUTRAL_RADIUS * pupilScale.value,
                    )
                    drawCircle(
                        color = Color(0xFF0A2414),
                        radius = pupilRadius + layerFit.mapSourceLength(3f),
                        center = pupilCenter,
                    )
                    drawCircle(
                        brush = Brush.radialGradient(
                            colors = listOf(
                                Color(0xFF000100),
                                Color(0xFF010302),
                                Color(0xFF07150C),
                            ),
                            center = pupilCenter,
                            radius = pupilRadius,
                        ),
                        radius = pupilRadius,
                        center = pupilCenter,
                    )

                    // The first Purkinje image belongs to the cornea/light, not to the iris.
                    drawSourceLayer(
                        image = catchlight,
                        sourceX = CATCHLIGHT_X + irisX * CATCHLIGHT_GAZE_FRACTION,
                        sourceY = CATCHLIGHT_Y + irisY * CATCHLIGHT_GAZE_FRACTION,
                        sourceWidth = CATCHLIGHT_SIZE,
                        sourceHeight = CATCHLIGHT_SIZE,
                        layerFit = layerFit,
                    )
                }

                // A single supplied intermediate frame covers the complete anatomy, avoiding
                // separate translucent iris/lid ghosts during the fast closing half.
                if (openToSquint > 0.001f) {
                    drawSourceLayer(
                        image = squintState,
                        sourceX = LIVING_EYE_STATE_X,
                        sourceY = LIVING_EYE_STATE_Y,
                        sourceWidth = LIVING_EYE_STATE_WIDTH,
                        sourceHeight = LIVING_EYE_STATE_HEIGHT,
                        layerFit = layerFit,
                        alpha = openToSquint,
                    )
                }
            } else {
                // Keep squint opaque as the base of the second half. Closed then replaces it,
                // so the permanently open foundation cannot shine through the eyelids.
                drawSourceLayer(
                    image = squintState,
                    sourceX = LIVING_EYE_STATE_X,
                    sourceY = LIVING_EYE_STATE_Y,
                    sourceWidth = LIVING_EYE_STATE_WIDTH,
                    sourceHeight = LIVING_EYE_STATE_HEIGHT,
                    layerFit = layerFit,
                )
                drawSourceLayer(
                    image = closedState,
                    sourceX = LIVING_EYE_STATE_X,
                    sourceY = LIVING_EYE_STATE_Y,
                    sourceWidth = LIVING_EYE_STATE_WIDTH,
                    sourceHeight = LIVING_EYE_STATE_HEIGHT,
                    layerFit = layerFit,
                    alpha = squintToClosed,
                )
            }
        }

        // Свет по внутренней кромке кольца — СНАРУЖИ клипа: он должен ложиться на бронзу, а не
        // подрезаться ею. Центр НАМЕРЕННО прозрачный: заливая середину, мы засветили бы радужку
        // и зрачок — самое ценное в кадре. Читается как свет из-под бронзы, а не как пятно сверху.
        val glowValue = glow.value
        if (glowValue > 0.01f) {
            val centre = Offset(size.width / 2f, size.height / 2f)
            val glowRadius = medallion / 2f - bronzeInset
            drawCircle(
                brush = Brush.radialGradient(
                    colorStops = arrayOf(
                        0f to Color.Transparent,
                        GLOW_INNER_EDGE to Color.Transparent,
                        // Промежуточная точка делает набор квадратичным: у кромки свет есть, а к
                        // радужке спадает быстро. Один линейный переход давал ореол поверх глаза.
                        (GLOW_INNER_EDGE + (1f - GLOW_INNER_EDGE) * 0.5f) to
                            GLOW_TINT.copy(alpha = GLOW_MAX_ALPHA * glowValue * 0.25f),
                        1f to GLOW_TINT.copy(alpha = GLOW_MAX_ALPHA * glowValue),
                    ),
                    center = centre,
                    radius = glowRadius,
                ),
                radius = glowRadius,
                center = centre,
            )
        }
    }
}

private suspend fun openForConnection(lid: Animatable<Float, AnimationVector1D>) {
    if (lid.value > 0.5f) {
        lid.animateTo(
            0.5f,
            tween(durationMillis = 140, easing = LinearOutSlowInEasing),
        )
    }
    lid.animateTo(
        0f,
        tween(durationMillis = 290, easing = LinearOutSlowInEasing),
    )
}

private suspend fun closeForDisconnect(lid: Animatable<Float, AnimationVector1D>) {
    if (lid.value < 0.5f) {
        lid.animateTo(
            0.5f,
            tween(durationMillis = 90, easing = FastOutLinearInEasing),
        )
    }
    lid.animateTo(
        1f,
        tween(durationMillis = 160, easing = FastOutSlowInEasing),
    )
}

private suspend fun blinkOnce(
    lid: Animatable<Float, AnimationVector1D>,
    eyeShift: Animatable<Float, AnimationVector1D>,
) {
    coroutineScope {
        launch {
            lid.animateTo(
                0.5f,
                tween(durationMillis = 34, easing = FastOutLinearInEasing),
            )
            lid.animateTo(
                1f,
                tween(durationMillis = 58, easing = FastOutLinearInEasing),
            )
        }
        launch {
            eyeShift.animateTo(
                1f,
                tween(durationMillis = 92, easing = FastOutLinearInEasing),
            )
        }
    }
    delay(26L)
    coroutineScope {
        launch {
            lid.animateTo(
                0.5f,
                tween(durationMillis = 80, easing = LinearOutSlowInEasing),
            )
            lid.animateTo(
                0f,
                tween(durationMillis = 125, easing = LinearOutSlowInEasing),
            )
        }
        launch {
            eyeShift.animateTo(
                0f,
                tween(durationMillis = 205, easing = LinearOutSlowInEasing),
            )
        }
    }
}

private suspend fun gazeTo(
    gazeX: Animatable<Float, AnimationVector1D>,
    gazeY: Animatable<Float, AnimationVector1D>,
    targetX: Float,
    targetY: Float,
    durationMillis: Int,
) = coroutineScope {
    launch {
        gazeX.animateTo(
            targetX,
            tween(durationMillis = durationMillis, easing = FastOutSlowInEasing),
        )
    }
    launch {
        gazeY.animateTo(
            targetY,
            tween(durationMillis = durationMillis, easing = FastOutSlowInEasing),
        )
    }
}

private fun Random.nextBlinkDelayMillis(): Long {
    val u1 = nextDouble().coerceAtLeast(0.000_001)
    val u2 = nextDouble()
    val gaussian = sqrt(-2.0 * ln(u1)) * cos(2.0 * PI * u2)
    val seconds = exp(ln(4.2) + 0.45 * gaussian).coerceIn(1.5, 9.5)
    return (seconds * 1_000.0).roundToLong()
}

private fun Random.nextFloat(from: Float, until: Float): Float =
    nextDouble(from.toDouble(), until.toDouble()).toFloat()

private fun DrawScope.drawSourceLayer(
    image: ImageBitmap,
    sourceX: Float,
    sourceY: Float,
    sourceWidth: Float,
    sourceHeight: Float,
    layerFit: LivingEyeLayerFit,
    alpha: Float = 1f,
) {
    val left = layerFit.mapSourceX(sourceX)
    val top = layerFit.mapSourceY(sourceY)
    val width = layerFit.mapSourceLengthX(sourceWidth)
    val height = layerFit.mapSourceLengthY(sourceHeight)
    drawImage(
        image = image,
        srcOffset = IntOffset.Zero,
        srcSize = IntSize(image.width, image.height),
        dstOffset = IntOffset(left.roundToInt(), top.roundToInt()),
        dstSize = IntSize(
            width.roundToInt().coerceAtLeast(1),
            height.roundToInt().coerceAtLeast(1),
        ),
        alpha = alpha.coerceIn(0f, 1f),
        filterQuality = FilterQuality.High,
    )
}

private fun eyeAperturePath(layerFit: LivingEyeLayerFit): Path = Path().apply {
    APERTURE_UPPER.forEachIndexed { index, point ->
        val mapped = sourcePoint(layerFit, point.x, point.y)
        if (index == 0) moveTo(mapped.x, mapped.y) else lineTo(mapped.x, mapped.y)
    }
    APERTURE_LOWER.asReversed().forEach { point ->
        val mapped = sourcePoint(layerFit, point.x, point.y)
        lineTo(mapped.x, mapped.y)
    }
    close()
}

private fun sourcePoint(layerFit: LivingEyeLayerFit, x: Float, y: Float): Offset = Offset(
    layerFit.mapSourceX(x),
    layerFit.mapSourceY(y),
)

private data class SourcePoint(val x: Float, val y: Float)

private const val SCLERA_X = 350f
private const val SCLERA_Y = 930f
private const val SCLERA_WIDTH = 660f
private const val SCLERA_HEIGHT = 280f

private const val IRIS_X = 535f
private const val IRIS_Y = 900f
private const val IRIS_SIZE = 292f
private const val PUPIL_CENTER_X = 681f
private const val PUPIL_CENTER_Y = 1045f
private const val PUPIL_NEUTRAL_RADIUS = 54f
private const val PUPIL_BRIGHT_SCALE = 43f / PUPIL_NEUTRAL_RADIUS
private const val PUPIL_DARK_SCALE = 66f / PUPIL_NEUTRAL_RADIUS

private const val CATCHLIGHT_X = 635f
private const val CATCHLIGHT_Y = 945f
private const val CATCHLIGHT_SIZE = 90f
private const val CATCHLIGHT_GAZE_FRACTION = 0.08f

private const val MAX_GAZE_X = 7f
private const val MAX_GAZE_Y = 4f
private const val BLINK_NASAL_SHIFT = 1.2f
private const val BLINK_DOWN_SHIFT = 2f

private val APERTURE_UPPER = listOf(
    SourcePoint(388f, 1083f),
    SourcePoint(405f, 1061f),
    SourcePoint(430f, 1037f),
    SourcePoint(460f, 1014f),
    SourcePoint(500f, 993f),
    SourcePoint(540f, 978f),
    SourcePoint(580f, 968f),
    SourcePoint(620f, 961f),
    SourcePoint(660f, 957f),
    SourcePoint(700f, 957f),
    SourcePoint(740f, 962f),
    SourcePoint(780f, 973f),
    SourcePoint(820f, 990f),
    SourcePoint(860f, 1011f),
    SourcePoint(900f, 1036f),
    SourcePoint(932f, 1061f),
    SourcePoint(957f, 1083f),
)

private val APERTURE_LOWER = listOf(
    SourcePoint(388f, 1083f),
    SourcePoint(420f, 1104f),
    SourcePoint(460f, 1123f),
    SourcePoint(500f, 1139f),
    SourcePoint(540f, 1152f),
    SourcePoint(580f, 1162f),
    SourcePoint(620f, 1170f),
    SourcePoint(660f, 1174f),
    SourcePoint(700f, 1172f),
    SourcePoint(740f, 1167f),
    SourcePoint(780f, 1159f),
    SourcePoint(820f, 1148f),
    SourcePoint(860f, 1133f),
    SourcePoint(900f, 1115f),
    SourcePoint(932f, 1098f),
    SourcePoint(957f, 1083f),
)

// Свечение: на подключении пульсирует между MIN и MAX, на «подключено» держится ровно.
private const val GLOW_CONNECTING_MIN = 0.45f
private const val GLOW_CONNECTING_MAX = 1f
private const val GLOW_CONNECTED = 0.7f
// Доля радиуса, до которой свет полностью прозрачен: центр не засвечиваем. 0.82 и alpha 0.22
// вместо прежних 0.55 и 0.5 — при них свет ложился пеленой поверх глаза (владелец 31.07).
private const val GLOW_INNER_EDGE = 0.82f
private const val GLOW_MAX_ALPHA = 0.22f
private val GLOW_TINT = Color(0xFF54FF8F)

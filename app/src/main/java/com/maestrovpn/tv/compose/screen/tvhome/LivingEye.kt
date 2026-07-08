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
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.res.imageResource
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import com.maestrovpn.tv.R
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.atan2
import kotlin.math.hypot
import kotlin.math.sin
import kotlin.random.Random

/**
 * Живой глаз в сокете медальона (только телефон). Арт = ПИКСЕЛИ мокапа владельца, разрезанные на
 * слои по анатомии (фидбек владельца 2026-07-08): home_eye_ball = ЯБЛОКО ЦЕЛИКОМ (белок+прожилки,
 * двигается как один шар — радужка не «отрывается» от белка), home_eye_iris2 = радужка БЕЗ зрачка,
 * зрачок = отдельный процедурный слой (расширяется И сужается), home_eye_catch = артовый катчлайт
 * (свет-анкерный, почти не движется), home_lid_up/lo = ВЕКИ ИЗ ЭСКИЗА с настоящими ресницами
 * (моргание = растяжка от якоря), home_plasma_core = ядро энергосферы. Поведение: спека владельца
 * (Белл, тайминг-асимметрия, зрачковый рефлекс, squash, wet-flash, слёзная плёнка, саккады).
 * В покое композиция совпадает с запечённым фоном (diff 1.1/255, проверено при раскройке).
 */
@Composable
fun LivingEye(
    connected: Boolean,
    eyeAlpha: () -> Float,          // кроссфейд фонов из TvHomeScreen (0=сфера, 1=глаз)
    touchDir: State<Offset?>,       // направление на палец в долях (-1..1) от центра сокета, null = нет касания
    modifier: Modifier = Modifier,
) {
    // СЛОИ ИЗ АРТА (фидбек владельца): яблоко ЦЕЛИКОМ (белок+прожилки, двигается как одно),
    // радужка БЕЗ зрачка (зрачок отдельный), артовый катчлайт (свет-анкерный), артовые ВЕКИ.
    val eyeBall = ImageBitmap.imageResource(R.drawable.home_eye_ball)
    val eyeIris2 = ImageBitmap.imageResource(R.drawable.home_eye_iris2)
    val eyeCatch = ImageBitmap.imageResource(R.drawable.home_eye_catch)
    val lidUp = ImageBitmap.imageResource(R.drawable.home_lid_up)
    val lidLo = ImageBitmap.imageResource(R.drawable.home_lid_lo)
    // ЯДРО энергии (r=198 из 234): вращается ТОЛЬКО оно — стеклянный купол с бликом
    // остаётся запечённым в фоне неподвижным (иначе блик «уезжает» = артефакт).
    val plasmaCore = ImageBitmap.imageResource(R.drawable.home_plasma_core)

    // взгляд в долях макс. сдвига (-1..1 по обеим осям, длина ≤1)
    val gaze = remember { Animatable(Offset.Zero, Offset.VectorConverter) }
    val lid = remember { Animatable(1f) }        // 1 = закрыт, 0 = открыт
    val squint = remember { Animatable(0f) }     // 0..1 лёгкий прищур (доля века)
    val dilate = remember { Animatable(0f) }     // 0 = зрачок как в арте, 1 = максимально расширен
    val greenGlint = remember { Animatable(0f) } // зелёный отблеск при подключении
    val spin = remember { Animatable(0f) }       // градусы вихря плазмы
    val postLight = remember { Animatable(0f) }  // §3b: сужение зрачка на свет после открытия века
    val wetFlash = remember { Animatable(0f) }   // §5: «влажная вспышка» блика после моргания
    val tear = remember { Animatable(0f) }       // §5: слёзная плёнка, растекающаяся после моргания
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
                        // «наблюдает» за местом касания: тремор фиксации как у настоящего глаза
                        repeat(Random.nextInt(1, 3)) {
                            delay(Random.nextLong(450, 900))
                            val g = gaze.value + randomDir(0.015f, 0.04f)
                            gaze.animateTo(Offset(g.x.coerceIn(-1f, 1f), g.y.coerceIn(-1f, 1f)),
                                tween(Random.nextInt(60, 90), easing = LinearOutSlowInEasing))
                        }
                        delay(Random.nextLong(700, 1400))
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
            postLight.snapTo(0f); wetFlash.snapTo(0f); tear.snapTo(0f)
            launch { spin.animateTo(spin.value + 480f, tween(1000, easing = CubicBezierEasing(0.15f, 0.55f, 0.35f, 1f))) }
            delay(260)                                     // силуэт проступает под открывающимися веками
            lid.animateTo(0f, tween(680, easing = CubicBezierEasing(0.25f, 0f, 0.2f, 1f)))
            launch { dilate.animateTo(1f, tween(400, easing = FastOutSlowInEasing)); dilate.animateTo(0.30f, tween(650)) }
            launch { greenGlint.animateTo(1f, tween(260)); greenGlint.animateTo(0f, tween(950)) }
            delay(Random.nextLong(500, 1000))              // первый фокус на пользователе

            // ЖИЗНЬ — три параллельных контура, каждый со случайностью (цикл не повторяется)
            coroutineScope {
                launch { // §1/§6: быстрое закрытие → пауза → медленное открытие; полу- и двойные моргания
                    suspend fun doBlink(half: Boolean) {
                        val depth = if (half) 0.5f + Random.nextFloat() * 0.2f else 1f
                        val v = 0.85f + Random.nextFloat() * 0.30f          // ±15% скорость
                        lid.animateTo(depth, tween((Random.nextInt(EyeTune.CLOSE_MS_MIN, EyeTune.CLOSE_MS_MAX) * v).toInt(),
                            easing = CubicBezierEasing(0.11f, 0f, 0.5f, 0f)))          // easeInQuad
                        delay(Random.nextLong(EyeTune.PAUSE_MS_MIN, EyeTune.PAUSE_MS_MAX))
                        if (!half) {
                            wetFlash.snapTo(1f); tear.snapTo(1f); postLight.snapTo(1f)
                            launch { wetFlash.animateTo(0f, tween(EyeTune.WET_MS)) }
                            launch { tear.animateTo(0f, tween(EyeTune.TEAR_MS)) }
                            launch { postLight.animateTo(0f, tween(EyeTune.PUPIL_LIGHT_MS, easing = LinearOutSlowInEasing)) }
                        }
                        lid.animateTo(0f, tween((Random.nextInt(EyeTune.OPEN_MS_MIN, EyeTune.OPEN_MS_MAX) * v).toInt(),
                            easing = CubicBezierEasing(0.215f, 0.61f, 0.355f, 1f)))    // easeOutCubic
                    }
                    while (true) {
                        delay(blinkGapMs())
                        if (Random.nextFloat() < EyeTune.SACC_PRE) microSaccade(gaze)   // §3c: fixation reset до
                        val half = Random.nextFloat() < EyeTune.HALF_CHANCE
                        doBlink(half)
                        if (!half && Random.nextFloat() < EyeTune.DOUBLE_CHANCE) {
                            delay(Random.nextLong(EyeTune.DOUBLE_GAP_MIN, EyeTune.DOUBLE_GAP_MAX)); doBlink(false)
                        }
                        if (Random.nextFloat() < EyeTune.SACC_POST) microSaccade(gaze)  // §3c: сдвиг после
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

        // §2 феномен Белла: при закрытии век яблоко уходит вверх и чуть наружу (smoothstep 0.3→1).
        // gzx/gzy = «эффективный» взгляд для всей отрисовки (взгляд + увод Белла).
        val bellT = ((lid.value - 0.3f) / 0.7f).coerceIn(0f, 1f)
        val bellS = bellT * bellT * (3f - 2f * bellT)
        val gzx = (gaze.value.x + EyeTune.BELL_OUT * bellS).coerceIn(-1f, 1f)
        val gzy = (gaze.value.y + EyeTune.BELL_UP * bellS).coerceIn(-1f, 1f)
        val lidK0 = (lid.value + squint.value * 0.9f + gzy.coerceAtLeast(0f) * 0.08f).coerceIn(0f, 1f)

        // ── плазменный вихрь (отключено/переходы): вращается только ЯДРО энергии,
        //    стеклянный купол/блик — неподвижный запечённый фон ──
        if (ea < 0.995f && spin.value % 360f != 0f) {
            val cr = r * (198f / 234f)
            rotate(spin.value, c) {
                drawImage(plasmaCore, dstOffset = IntOffset((c.x - cr).toInt(), (c.y - cr).toInt()),
                    dstSize = IntSize((2 * cr).toInt(), (2 * cr).toInt()),
                    alpha = 1f - ea, filterQuality = FilterQuality.Medium)
            }
        }
        if (ea < 0.005f) return@Canvas

        val socket = Path().apply { addOval(Rect(c.x - r, c.y - r, c.x + r, c.y + r)) }
        // Кривые краёв АРТОВЫХ век (сняты с эскиза): верх −0.46+0.09·nx², низ +0.64−0.12·nx².
        // «Окно» = зона глазного яблока между веками; яблоко/радужка/зрачок живут только в нём.
        val window = Path().apply {
            moveTo(c.x - r, c.y - 0.37f * r)
            quadraticBezierTo(c.x, c.y - 0.55f * r, c.x + r, c.y - 0.37f * r)
            lineTo(c.x + r, c.y + 0.52f * r)
            quadraticBezierTo(c.x, c.y + 0.76f * r, c.x - r, c.y + 0.52f * r)
            close()
        }
        clipPath(socket) {
            // §4 squash: веки давят на яблоко — микро-сплющивание глаза (не век)
            val sqK = ((lidK0 - 0.7f) / 0.3f).coerceIn(0f, 1f)
            withTransform({ if (sqK > 0f) scale(1f + EyeTune.SQUASH_X * sqK, 1f - EyeTune.SQUASH_Y * sqK, pivot = c) }) {
            clipPath(window) {
                // ЯБЛОКО ЦЕЛИКОМ (белок+прожилки+лимб) едет со взглядом — радужка «не отрывается»
                // от белка по построению: они один движущийся шар под статичными веками.
                val maxShift = r * 0.115f
                val ballShift = Offset(gzx * maxShift, gzy * maxShift)
                val ballR = r * (246f / 228f)
                val ballC = c + ballShift
                drawImage(eyeBall, dstOffset = IntOffset((ballC.x - ballR).toInt(), (ballC.y - ballR).toInt()),
                    dstSize = IntSize((2 * ballR).toInt(), (2 * ballR).toInt()),
                    alpha = ea, filterQuality = FilterQuality.Medium)

                // РАДУЖКА (зрачок в ней заинпейнчен — он отдельным слоем ниже): тот же сдвиг, что
                // и яблоко, плюс перспектива роговицы вдоль направления взгляда.
                val irisC = c + Offset(7f / 228f * r, 10f / 228f * r) + ballShift
                val irisR = r * (158f / 228f)
                val gazeLen = hypot(gzx, gzy).coerceAtMost(1f)
                val gazeDeg = Math.toDegrees(atan2(gzy, gzx).toDouble()).toFloat()
                withTransform({
                    if (gazeLen > 0.01f) {
                        rotate(gazeDeg, irisC)
                        scale(1f - 0.20f * gazeLen, 1f, pivot = irisC)
                        rotate(-gazeDeg, irisC)
                    }
                }) {
                    drawImage(eyeIris2, dstOffset = IntOffset((irisC.x - irisR).toInt(), (irisC.y - irisR).toInt()),
                        dstSize = IntSize((2 * irisR).toInt(), (2 * irisR).toInt()),
                        alpha = ea, filterQuality = FilterQuality.Medium)
                }

                // лёгкая мир-анкерная тень кривизны на переднем крае (довесок объёма)
                if (gazeLen > 0.01f) {
                    val u = Offset(gzx / gazeLen, gzy / gazeLen)
                    val leadC = irisC + u * (irisR * 1.02f)
                    drawCircle(brush = Brush.radialGradient(
                        listOf(Color.Black.copy(alpha = 0.16f * gazeLen * ea), Color.Transparent),
                        center = leadC, radius = irisR * 0.8f), radius = irisR * 0.8f, center = leadC)
                }

                // ЗРАЧОК — ОТДЕЛЬНЫЙ СЛОЙ (§3): может и расширяться, и СУЖАТЬСЯ ниже артового;
                // ведёт за взглядом на 18% (преломление роговицы).
                val pupilC = irisC + Offset(gzx, gzy) * (maxShift * 0.18f)
                val pupilR = r * (45f / 228f) * (1f + 0.38f * dilate.value + breath.coerceAtLeast(-0.06f)
                    + EyeTune.PUPIL_DARK * lid.value - EyeTune.PUPIL_LIGHT * postLight.value)
                drawCircle(brush = Brush.radialGradient(
                        0f to Color(0xFF020603), 0.72f to Color(0xFF020603), 1f to Color(0x00020603),
                        center = pupilC, radius = pupilR * 1.16f),
                    radius = pupilR * 1.16f, center = pupilC, alpha = ea)

                // КАТЧЛАЙТ ИЗ АРТА: отражение света на роговице — стоит на месте (лёгкий противоход
                // взгляду), медленно дрейфует, вспыхивает при открытии века (§5 wet flash).
                val wetK = (1f + EyeTune.WET_FLASH * wetFlash.value)
                val catchC = c + Offset(5f / 228f * r, 1f / 228f * r) -
                    Offset(gzx, gzy) * (maxShift * 0.15f) + Offset(glintDrift * 2f, glintDrift * 1.2f)
                val catchR = r * (40f / 228f) * (1f + 0.10f * wetFlash.value)
                drawImage(eyeCatch, dstOffset = IntOffset((catchC.x - catchR).toInt(), (catchC.y - catchR).toInt()),
                    dstSize = IntSize((2 * catchR).toInt(), (2 * catchR).toInt()),
                    alpha = (ea * wetK).coerceAtMost(1f), filterQuality = FilterQuality.Medium)

                // §5 слёзная плёнка: 200-400мс после моргания — влажная полоса внизу роговицы
                if (tear.value > 0.01f) {
                    val ty = irisC.y + irisR * 0.35f
                    drawRect(brush = Brush.verticalGradient(
                            0f to Color.Transparent, 0.5f to Color.White.copy(alpha = 0.075f * tear.value * ea),
                            1f to Color.Transparent, startY = ty, endY = ty + irisR * 0.5f),
                        topLeft = Offset(irisC.x - irisR * 0.8f, ty),
                        size = androidx.compose.ui.geometry.Size(irisR * 1.6f, irisR * 0.5f))
                }

                // подключение: радужка светлеет волной + зелёный отблеск по ободу
                if (greenGlint.value > 0.01f) {
                    drawCircle(brush = Brush.radialGradient(
                            0f to Color(0xFFCFFFE0).copy(alpha = 0.16f * greenGlint.value),
                            0.75f to Color(0xFF9FF7C0).copy(alpha = 0.10f * greenGlint.value),
                            1f to Color.Transparent, center = irisC, radius = irisR),
                        radius = irisR, center = irisC)
                    drawCircle(brush = Brush.radialGradient(
                            0.35f to Color(0x0034E67A), 0.8f to Color(0xFF34E67A).copy(alpha = 0.30f * greenGlint.value),
                            1f to Color.Transparent, center = c, radius = r),
                        radius = r, center = c)
                }
            } // конец окна яблока
            } // конец squash

            // ── ВЕКИ ИЗ ЭСКИЗА (фидбек владельца): артовые дуги с ресницами. Моргание =
            //    вертикальная растяжка от якоря (верх дуги / низ валика): край с НАСТОЯЩИМИ
            //    ресницами едет к линии смыкания (+0.50R), нижний валик ведомый (~15%). ──
            val lidLayerR = 234f / 228f * r
            val srcHUp = (0.90f - 0.46f) * r
            val scaleUp = (srcHUp + 0.96f * r * lidK0) / srcHUp
            withTransform({ scale(1f, scaleUp, pivot = Offset(c.x, c.y - 0.90f * r)) }) {
                drawImage(lidUp, dstOffset = IntOffset((c.x - lidLayerR).toInt(), (c.y - lidLayerR).toInt()),
                    dstSize = IntSize((2 * lidLayerR).toInt(), (2 * lidLayerR).toInt()),
                    alpha = ea, filterQuality = FilterQuality.Medium)
            }
            val srcHLo = (0.97f - 0.64f) * r
            val scaleLo = (srcHLo + 0.14f * r * lidK0) / srcHLo
            withTransform({ scale(1f, scaleLo, pivot = Offset(c.x, c.y + 0.97f * r)) }) {
                drawImage(lidLo, dstOffset = IntOffset((c.x - lidLayerR).toInt(), (c.y - lidLayerR).toInt()),
                    dstSize = IntSize((2 * lidLayerR).toInt(), (2 * lidLayerR).toInt()),
                    alpha = ea, filterQuality = FilterQuality.Medium)
            }
        }
    }
}

/** §7: все параметры моргания/жизни глаза в одном месте (спека владельца, ЧАСТЬ 2). */
private object EyeTune {
    // §1 тайминг: закрытие быстрое (ease-in), пауза, открытие медленнее (ease-out)
    const val CLOSE_MS_MIN = 80; const val CLOSE_MS_MAX = 120
    const val PAUSE_MS_MIN = 50L; const val PAUSE_MS_MAX = 100L
    const val OPEN_MS_MIN = 150; const val OPEN_MS_MAX = 250
    // §6 интервалы: колокол μ≈4с в пределах 2-6с; двойные 15%; полу-моргания 20%
    const val GAP_MIN_MS = 2000L; const val GAP_MAX_MS = 6000L
    const val DOUBLE_CHANCE = 0.15f; const val DOUBLE_GAP_MIN = 250L; const val DOUBLE_GAP_MAX = 400L
    const val HALF_CHANCE = 0.20f
    // §2 феномен Белла: вверх до ~5°, наружу до ~2° (в долях нашего хода взгляда)
    const val BELL_UP = -0.30f; const val BELL_OUT = 0.10f
    // §3b зрачок: расширение за закрытым веком + сужение на свет после открытия
    const val PUPIL_DARK = 0.07f; const val PUPIL_LIGHT = 0.13f; const val PUPIL_LIGHT_MS = 320
    // §3c микросаккады до/после моргания
    const val SACC_PRE = 0.40f; const val SACC_POST = 0.60f
    // §4 squash яблока при blink>0.7
    const val SQUASH_X = 0.02f; const val SQUASH_Y = 0.05f
    // §5 влажность: вспышка блика при открытии + слёзная плёнка
    const val WET_FLASH = 0.45f; const val WET_MS = 170; const val TEAR_MS = 380
}

/** §6: интервал между морганиями — колокол (сумма равномерных) μ≈4с, в пределах 2-6с. */
private fun blinkGapMs(): Long {
    val g = (Random.nextFloat() + Random.nextFloat() + Random.nextFloat()) / 3f
    return EyeTune.GAP_MIN_MS + (g * (EyeTune.GAP_MAX_MS - EyeTune.GAP_MIN_MS)).toLong()
}

/** §3c: микросаккада — крохотный быстрый сдвиг взгляда (fixation reset). */
private suspend fun microSaccade(gaze: Animatable<Offset, *>) {
    val j = gaze.value + randomDir(0.02f, 0.05f)
    gaze.animateTo(
        Offset(j.x.coerceIn(-1f, 1f), j.y.coerceIn(-1f, 1f)),
        tween(Random.nextInt(55, 85), easing = LinearOutSlowInEasing),
    )
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

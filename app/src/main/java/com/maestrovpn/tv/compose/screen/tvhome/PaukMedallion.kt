package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.rememberIsLowRam
import com.maestrovpn.tv.compose.theme.NeonGreen
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.sin

// ============================================================================
//  MaestroVPN — процедурный 2D-паук (порт из build_final_spider.py).
//  Тело = спрайт pauk_body, 8 лап + 2 педипальпы = реальная лапа-текстура pauk_leg,
//  размещённая бедро→стопа матрицей (поворот+масштаб). 2D-плантинг стоп (без скольжения),
//  чередующийся тетрапод, перемещение с ПОВОРОТОМ тела к ходу (v19) + leg-driven brake,
//  повадки покоя (look/groom/crouch/RISE/shuffle/turn), покачивание (sway ±3°, бёдра качаются
//  ВМЕСТЕ с телом), 3-слойная мягкая направленная тень, медальон.
//  Симуляция крутится на withFrameNanos → перерисовка каждый кадр БЕЗ рекомпозиции.
// ============================================================================

// --- геометрия спрайтов (px) ---
private const val PAUK_BODY_W = 176f
private const val PAUK_BODY_H = 390f
private const val PAUK_LEG_W = 87f
private const val PAUK_LEG_H = 388f

// пивот (бедро) и кончик (стопа) в долях лапа-битмапа (правая лапа, из Python-builder)
private const val LEG_PIV_XF = 0.5855f
private const val LEG_PIV_YF = 0.0f
private const val LEG_TIP_XF = 0.1572f
private const val LEG_TIP_YF = 0.997f

// «эталонное тело» = 200px, как в JS: локальные px лап заданы в этой системе, экран через SC.
private const val BODY_REF = 200f
// увеличение паука (owner: «увеличиваем ещё в 2 раза, разрешаем ВСЕ движения, выползает ЗА
// кнопку, но охраняет кнопку»). Паук теперь рисуется в БОЛЬШОМ поле БЕЗ круглого клипа (лапы
// свободно выходят за кольцо-кнопку на тёмный фон) и ХОДИТ вокруг кнопки. Масштаб = доля поля;
// вместе с увеличенным полем даёт ~2× от прежнего размера. Тюнить одним числом.
// Медальон подложки пересобран под макет (кольцо меньше, зелёный диск ≈0.53W) → паук ужат под него.
// Поле паука в backdrop-режиме теперь тянется на ширину экрана → масштаб паука ТРЕКАЕТ кольцо на всех
// телефонах (см. fieldMod ниже). 1.32 = лапы достают ~0.77 радиуса диска (как на макете). Было 2.35.
private const val SPIDER_SCALE = 1.32f
// насколько далеко от центра кнопки паук отходит, «охраняя» её (доля радиуса поля) — виден
// как ходьба вокруг кнопки, но держится рядом. Больше = гуляет дальше (лапы уходят к краю экрана).
private const val GUARD_LEASH = 0.14f
private val BLACK_TINT = ColorFilter.tint(Color.Black)   // тень: один общий фильтр, не аллоцируем на кадр

// --- v19 ГНУЩИЕСЯ ЛАПЫ (mesh-warp, порт из принятого owner прототипа emerald_proto ?v=19) ---
// Лапа = единый спрайт, натянутый на сетку из NRIB поперечных рёбер, гнётся вдоль 2-коленной
// АРКИ (femur→tibia→metatarsus/tarsus, оба колена наружу от тела). Рисуется нативным
// drawBitmapMesh (аппаратно). Длина лапы = reach*SLACK > дистанции стопы → колено ВСЕГДА согнуто.
private const val NRIB = 13
private const val LEG_SLACK = 1.40f     // длина/дистанция → выраженное колено (ratio ~0.71)
private const val LEG_WID = 1.25f       // толщина (читается сегментами, не волосок)
private const val KH1 = 0.32f           // главное колено femur-patella: оффсет наружу (доля Lg)
private const val KH2 = 0.17f           // второй излом tibia-metatarsus (дистал изгибается к стопе)
private const val KF1 = 0.40f           // доля хорды hip→foot до k1
private const val KF2 = 0.72f           // доля хорды до k2
private const val LEG_SMOOTH = 2        // проходов сглаживания угла колена (меньше = острее кинк)
private const val MESH_DENSE = 160      // ёмкость буфера плотной полилинии пути

// фазы жизненного цикла паука (выход из-под кнопки / охрана / уход под кнопку)
private const val PH_HIDDEN = 0
private const val PH_EMERGING = 1
private const val PH_GUARDING = 2
private const val PH_HIDING = 3

// диагональные четвёрки тетрапода (шагают в противофазе) — хоистнуты, не аллоцируем на кадр
private val GAIT_A = intArrayOf(0, 3, 4, 7)
private val GAIT_B = intArrayOf(1, 2, 5, 6)

// анатомия лап: [fx,fy(доля спрайта тела), угол°, reach(px)] — правая; левая зеркалится.
// fx/fy взяты из LEGDEF (build_final_spider.py); reach = последний столбец LEGDEF.
private val LEGDEF = arrayOf(
    floatArrayOf(0.72f, 0.14f, 44f, 150f),   // I  перед  (v19: расширена+укорочена 32°/172→44°/150, чтоб не иглой)
    floatArrayOf(0.84f, 0.25f, 68f, 146f),   // II
    floatArrayOf(0.85f, 0.37f, 104f, 130f),  // III       (самая короткая)
    floatArrayOf(0.75f, 0.47f, 150f, 168f),  // IV зад     (длинная)
)

private val EYE_COLOR = Color(0xFFFFB84D)

/** Одна лапа: стопа приклеена в мире, пока дрейф не превысит порог → взмах по smoothstep. */
private class SimLeg(
    val side: Int,      // +1 право / -1 лево
    val hlx: Float,     // локальные px крепления (тело 200px, центр = 0)
    val hly: Float,
    val ang: Float,     // базовый угол лапы (рад), от «вверх»; мир = headingWorld + ang
    val reach: Float,   // досягаемость в «200px-тело» px
) {
    var footX = 0f
    var footY = 0f
    var moving = false
    var t = 0f
    var dur = 0.16f
    var fromX = 0f
    var fromY = 0f
    var toX = 0f
    var toY = 0f
    var stepH = 0f      // не используем для сдвига стопы (нет вертикали в 2D), но храним амплитуду взмаха
    var lift = 0f       // 0..1 фаза подъёма (для micro-укорочения при взмахе)
}

/** 2 передние педипальпы — та же лапа-текстура, короткий reach, стопа осциллирует («щупают»). */
private class Palp(
    val side: Int,
    val hlx: Float,
    val hly: Float,
    val ang: Float,
    val reach: Float,
    val ph: Float,
) {
    var footX = 0f
    var footY = 0f
}

private fun rnd(a: Float, b: Float) = a + (Math.random().toFloat()) * (b - a)
private fun lerp(a: Float, b: Float, t: Float) = a + (b - a) * t
private fun clamp(v: Float, a: Float, b: Float) = if (v < a) a else if (v > b) b else v
private fun lerpAngle(a: Float, b: Float, t: Float): Float {   // поворот по кратчайшей дуге
    var d = b - a
    while (d > PI.toFloat()) d -= 2f * PI.toFloat()
    while (d < -PI.toFloat()) d += 2f * PI.toFloat()
    return a + d * t
}
private fun dist(ax: Float, ay: Float, bx: Float, by: Float) = hypot(ax - bx, ay - by)

/**
 * Вся симуляция паука. remember { } держит мутабельное состояние; update(dt) двигает.
 * Ничего от Compose внутри не зависит — рисуем из drawBehind, читая поля напрямую.
 */
private class SpiderSim {
    // мир задаётся при первом update по размеру Canvas
    var cx = 0f          // центр медальона
    var cy = 0f
    var radius = 0f      // радиус медальона (px)
    var sc = 1f          // общий масштаб паука
    private var inited = false

    // передаются из withFrameNanos, читаются в update() внутри drawBehind
    var pendingDt = 0.016f
    var pendingConnected = false

    // тело
    var x = 0f
    var y = 0f
    var heading = -(PI.toFloat() / 2f)                            // куда «смотрит» тело — ПОВОРАЧИВАЕТСЯ к ходу (v19)
    val headingWorld: Float get() = heading + sway                // фактическое направление тела (+покачивание)
    var vx = 0f
    var vy = 0f
    var crouch = 1f
    var crouchTarget = 1f
    var rise = 0f
    var riseTarget = 0f
    var sway = 0f
    var breathe = 0f
    var bt = 0f          // глобальные часы тела

    // патруль
    var targetX = 0f
    var targetY = 0f
    var pause = 0f
    var walking = false
    var idleT = 0f

    // повадки покоя
    private var tLook = rnd(3f, 5f)
    private var tGroom = rnd(8f, 15f)
    private var tCrouch = rnd(10f, 20f)
    private var tShuf = rnd(5f, 10f)
    private var tTurn = rnd(4f, 7f)
    private var tRise = rnd(8f, 13f)
    private var act: String? = null
    private var actT = 0f
    private var actD = 0f
    private var groomLeg = 0
    private var shSide = 1

    val legs = ArrayList<SimLeg>(8)
    val palps = ArrayList<Palp>(2)

    // тень догоняет тело
    var shadowX = 0f
    var shadowY = 0f

    // ── фазы «выход/охрана/уход» (owner: выползает из-под кнопки при подключении, охраняет,
    //    заползает под кнопку при отключении). HIDDEN=спрятан под кнопкой (не рисуем),
    //    EMERGING=выходит к центру, GUARDING=охраняет (блуждает у кнопки), HIDING=уходит вниз. ──
    var phase = PH_HIDDEN
    var hideY = 0f            // «под кнопкой» = ниже поля, невидим
    var emergeAlpha = 1f      // fade при выходе/уходе (0=спрятан, 1=на кнопке)
    private var started = false
    private var prevConnected = false

    private fun buildRig() {
        legs.clear()
        for (d in LEGDEF) {
            val fx = d[0]; val fy = d[1]; val deg = d[2]; val reach = d[3]
            for (s in intArrayOf(1, -1)) {
                val hx = ((fx - 0.5f) * PAUK_BODY_W) * (BODY_REF / PAUK_BODY_H) * s
                val hy = (fy - 0.5f) * BODY_REF
                val ang = (if (s > 0) deg else -deg) * (PI.toFloat() / 180f)
                legs.add(SimLeg(s, hx, hy, ang, reach))
            }
        }
        palps.clear()
        palps.add(Palp(1, 9f, -84f, 0.40f, 52f, 0.0f))
        palps.add(Palp(-1, -9f, -84f, -0.40f, 52f, 2.1f))
    }

    /** Локальные px «200px-тела» → мир: поворот на heading(+sway) + масштаб SC·scale(rise/crouch). */
    fun toWorld(lx: Float, ly: Float): Offset {
        // heading = -90°; +90° приводит «вверх» тела к экранному верху; +sway — покачивание
        val a = headingWorld + (PI.toFloat() / 2f)   // = sway (тело смотрит вверх)
        val c = cos(a); val s = sin(a)
        val k = crouch
        return Offset(
            x + (lx * c - ly * s) * sc * k,
            y + (lx * s + ly * c) * sc * k,
        )
    }

    fun hipW(leg: SimLeg): Offset = toWorld(leg.hlx, leg.hly)

    /** Целевая (rest) позиция стопы в мире: бедро + dir(heading+legAngle)·reach. */
    fun restFoot(leg: SimLeg): Offset {
        val h = hipW(leg)
        val a = headingWorld + leg.ang
        val r = leg.reach * sc * crouch
        return Offset(h.x + cos(a) * r, h.y + sin(a) * r)
    }

    fun hipWPalp(p: Palp): Offset = toWorld(p.hlx, p.hly)

    private fun startSwing(leg: SimLeg) {
        val spd = hypot(vx, vy)
        leg.moving = true
        leg.t = 0f
        leg.dur = rnd(0.13f, 0.20f) / (if (spd > 110f) 1.4f else 1f)
        val rest = restFoot(leg)
        leg.fromX = leg.footX
        leg.fromY = leg.footY
        // цель = rest + velocity-lead (нога ставится чуть впереди по ходу тела)
        var tox = rest.x + vx * 0.10f
        var toy = rest.y + vy * 0.10f
        // ДЛИНА ШАГА ОГРАНИЧЕНА → семенит, не «летит»
        val dx = tox - leg.fromX; val dy = toy - leg.fromY
        val dd = hypot(dx, dy); val mx = 58f * sc
        if (dd > mx) { tox = leg.fromX + dx / dd * mx; toy = leg.fromY + dy / dd * mx }
        leg.toX = tox; leg.toY = toy
        leg.stepH = (20f + rnd(-4f, 8f)) * sc
    }

    /** Чередующийся тетрапод: диагональные четвёрки шагают в противофазе, когда дрейф > порога. */
    private fun strain(g: IntArray): Float {
        var s = 0f
        for (i in g) { val L = legs[i]; val r = restFoot(L); s += dist(L.footX, L.footY, r.x, r.y) }
        return s / g.size
    }
    private fun gait() {
        for (L in legs) if (L.moving) return
        val sA = strain(GAIT_A); val sB = strain(GAIT_B); val th = 20f * sc
        if (sA > th && sA >= sB) for (i in GAIT_A) startSwing(legs[i])
        else if (sB > th) for (i in GAIT_B) startSwing(legs[i])
    }

    /** Экстренный до-шаг: улетевшая слишком далеко лапа шагает вне очереди (тело догоняют волной).
     *  Без аллокаций (вызывается каждый кадр): считаем движущиеся, потом до `budget` раз берём
     *  самую дальнюю не-движущуюся лапу за порогом. */
    private fun emergencyStep() {
        val em = 44f * sc
        var moving = 0
        for (L in legs) if (L.moving) moving++
        var budget = (3 - moving).coerceAtLeast(0)
        while (budget > 0) {
            var best: SimLeg? = null; var bestD = em
            for (L in legs) if (!L.moving) {
                val r = restFoot(L); val d = dist(L.footX, L.footY, r.x, r.y)
                if (d > bestD) { bestD = d; best = L }
            }
            val b = best ?: return
            startSwing(b); budget--
        }
    }

    private fun behaviorReset() {
        tLook = rnd(3f, 5f); tGroom = rnd(8f, 15f); tCrouch = rnd(10f, 20f)
        tShuf = rnd(5f, 10f); tTurn = rnd(4f, 7f); tRise = rnd(8f, 13f)
    }

    private fun begin(a: String, d: Float) { act = a; actT = d; actD = d }

    private fun behaviorUpdate(dt: Float) {
        if (walking) return
        val cur = act
        if (cur != null) {
            actT -= dt
            when (cur) {
                "look" -> {
                    // лёгкий взгляд — сдвигаем стопу передней лапы (без разворота тела)
                    if (Math.random() < dt * 4f) { val L = legs[if (Math.random() < 0.5) 0 else 4]; if (!L.moving) startSwing(L) }
                }
                "groom" -> {
                    val L = legs[groomLeg]
                    val m = toWorld(0f, -PAUK_BODY_H * 0.42f * (BODY_REF / PAUK_BODY_H))
                    L.moving = false
                    val o = sin(actT * 18f) * 6f * sc
                    L.footX = lerp(L.footX, m.x + o, dt * 10f)
                    L.footY = lerp(L.footY, m.y, dt * 10f)
                }
                "crouch" -> { val p = 1f - actT / actD; crouchTarget = if (p < 0.5f) 0.90f else 1.0f }
                "shuffle" -> {
                    if (Math.random() < dt * 10f) {
                        val c = legs.filter { it.side == shSide && !it.moving }
                        if (c.isNotEmpty()) startSwing(c[(Math.random() * c.size).toInt()])
                    }
                }
                "turn" -> { /* лёгкий взгляд — тело не крутится; ничего не двигаем (heading фиксирован) */ }
                "rise" -> {
                    val p = 1f - actT / actD
                    riseTarget = if (p > 0.22f && p < 0.80f) 1f else 0f
                    if (Math.random() < dt * 2.5f) { val L = legs[(Math.random() * 8).toInt()]; if (!L.moving) startSwing(L) }
                }
            }
            if (actT <= 0f) { act = null; crouchTarget = 1f; riseTarget = 0f }
            return
        }
        tLook -= dt; tGroom -= dt; tCrouch -= dt; tShuf -= dt; tTurn -= dt; tRise -= dt
        when {
            tLook <= 0f -> { tLook = rnd(3f, 5f); begin("look", rnd(1f, 2f)) }
            tRise <= 0f -> { tRise = rnd(8f, 13f); begin("rise", rnd(1.3f, 1.9f)) }
            tGroom <= 0f -> { tGroom = rnd(8f, 15f); groomLeg = if (Math.random() < 0.5) 0 else 4; begin("groom", rnd(2f, 3f)) }
            tCrouch <= 0f -> { tCrouch = rnd(10f, 20f); begin("crouch", 1.0f) }
            tShuf <= 0f -> { tShuf = rnd(5f, 10f); shSide = if (Math.random() < 0.5) 1 else -1; begin("shuffle", rnd(1f, 2f)) }
            tTurn <= 0f -> { tTurn = rnd(4f, 7f); begin("turn", rnd(2f, 3f)) }
        }
    }

    private fun ensureInit(w: Float, h: Float) {
        if (inited) return
        inited = true
        cx = w / 2f; cy = h / 2f
        radius = (if (w < h) w else h) * 0.5f
        // масштаб: sc = сколько экранных px в 1 «200px-тело» px. Паук КРУПНЫЙ и живёт в поле шире
        // кольца-кнопки → лапы свободно выходят ЗА кнопку (клипа нет). Ходит вокруг, охраняя кнопку.
        sc = (radius * 0.34f) / 172f * SPIDER_SCALE
        hideY = cy + radius * 1.15f      // «под кнопкой»: ниже поля → полностью скрыт
        x = cx; y = cy
        targetX = cx; targetY = cy
        shadowX = cx; shadowY = cy
        buildRig()
        for (L in legs) { val r = restFoot(L); L.footX = r.x; L.footY = r.y }
        for (p in palps) { val hp = hipWPalp(p); p.footX = hp.x; p.footY = hp.y }
        behaviorReset()
    }

    fun update(dt: Float, w: Float, h: Float, connected: Boolean) {
        ensureInit(w, h)
        bt += dt

        // leg-driven brake: тело не обгоняет лапы (лечит «коньковое» скольжение) — из v19
        var maxStretch = 0f
        for (L in legs) {
            val hp = hipW(L); val d = dist(L.footX, L.footY, hp.x, hp.y) / (L.reach * sc).coerceAtLeast(1e-4f)
            if (d > maxStretch) maxStretch = d
        }
        val brk = if (maxStretch > 1.0f) (1f - (maxStretch - 1.0f) / 0.35f).coerceAtLeast(0.10f) else 1f

        // первый кадр: подключён → сразу охраняем в центре; иначе спрятан под кнопкой (не видно)
        if (!started) {
            started = true; prevConnected = connected
            if (connected) { phase = PH_GUARDING; x = cx; y = cy } else {
                phase = PH_HIDDEN; x = cx; y = hideY; walking = false
                for (L in legs) { val r = restFoot(L); L.footX = r.x; L.footY = r.y }
                for (p in palps) { val hp = hipWPalp(p); p.footX = hp.x; p.footY = hp.y }
            }
        }
        // фронт подключения → ВЫХОД из-под кнопки; отключения → УХОД под кнопку
        if (connected != prevConnected) {
            prevConnected = connected
            walking = true; idleT = 0f
            if (connected) { phase = PH_EMERGING; targetX = cx; targetY = cy }
            else { phase = PH_HIDING; targetX = cx; targetY = hideY }
        }

        if (walking) {
            val dx = targetX - x; val dy = targetY - y; val d = hypot(dx, dy)
            if (d < 10f) {
                walking = false; vx = 0f; vy = 0f; idleT = 0f
                when (phase) {
                    PH_EMERGING -> { phase = PH_GUARDING; behaviorReset() }   // вышел → охраняет
                    // ушёл под кнопку → прижимаем точно к hideY, чтобы emergeAlpha дошёл до 0 (без призрака)
                    PH_HIDING -> { phase = PH_HIDDEN; x = cx; y = hideY }
                    else -> behaviorReset()                                  // дошёл в патруле охраны
                }
            } else {
                if (pause > 0f) { pause -= dt; vx *= 0.85f; vy *= 0.85f } else {
                    if (phase == PH_GUARDING && Math.random() < dt * 0.4f) pause = rnd(1f, 2f)
                    // ОМНИнаправленно к цели БЕЗ поворота тела; выход/уход чуть быстрее патруля
                    val base = if (phase == PH_GUARDING) rnd(34f, 52f) else rnd(58f, 78f)
                    val a = atan2(dy, dx); val spd = base * sc * brk   // тормозим, если лапы растянуты
                    vx = lerp(vx, cos(a) * spd, dt * 2.5f)
                    vy = lerp(vy, sin(a) * spd, dt * 2.5f)
                }
            }
            gait()
        } else if (phase == PH_GUARDING) {
            idleT += dt
            behaviorUpdate(dt)
            // ОХРАНЯЕТ кнопку: неспешно прохаживается ВОКРУГ неё (тело не крутится, лапы веером)
            if (idleT > rnd(3f, 6f) && Math.random() < dt * 0.5f) {
                idleT = 0f; walking = true
                val ta = rnd(0f, (2f * PI).toFloat()); val r = radius * rnd(0.10f, 0.26f)
                var tx = x + cos(ta) * r; var ty = y + sin(ta) * r
                val ddx = tx - cx; val ddy = ty - cy; val dd = hypot(ddx, ddy); val lim = radius * GUARD_LEASH
                if (dd > lim) { tx = cx + ddx / dd * lim; ty = cy + ddy / dd * lim }
                targetX = tx; targetY = ty
            }
            if (hypot(vx, vy) < 3f && Math.random() < dt * 0.5f) {
                val L = legs[(Math.random() * 8).toInt()]; if (!L.moving) startSwing(L)
            }
        }
        // PH_HIDDEN: спрятан под кнопкой — стоит, ничего не делаем (emergeAlpha=0 → не рисуется)

        // физика тела
        x += vx * dt; y += vy * dt
        // «поводок» к кнопке — ТОЛЬКО в охране (при выходе/уходе паук должен дойти до центра/hideY)
        if (phase == PH_GUARDING) {
            val ddx = x - cx; val ddy = y - cy; val dd = hypot(ddx, ddy); val lim = radius * GUARD_LEASH
            if (dd > lim) { x = cx + ddx / dd * lim; y = cy + ddy / dd * lim }
        }
        // тело ПОВОРАЧИВАЕТСЯ к направлению хода; в покое плавно возвращается «головой вверх» (v19)
        val movespd = hypot(vx, vy)
        heading = if (movespd > radius * 0.10f) lerpAngle(heading, atan2(vy, vx), dt * 1.6f * brk)
                  else lerpAngle(heading, -(PI.toFloat() / 2f), dt * 2.4f)
        crouch = lerp(crouch, crouchTarget, dt * 4f)
        rise = lerp(rise, riseTarget, dt * 3.5f)
        breathe = sin(bt * 0.3f * (2f * PI).toFloat()) * 0.01f
        sway = sin(bt * 1.6f) * 0.05f   // ±~3° покачивание; hip‑world учитывает +sway (toWorld)

        // педипальпы постоянно «щупают»
        for (p in palps) {
            val feel = sin(bt * 2.6f + p.ph)
            val a = headingWorld + p.ang + feel * 0.34f
            val r = p.reach * sc * (0.78f + 0.22f * sin(bt * 3.4f + p.ph))
            val hp = hipWPalp(p)
            p.footX = hp.x + cos(a) * r
            p.footY = hp.y + sin(a) * r
        }

        emergencyStep()
        // взмахи лап
        for (L in legs) {
            if (L.moving) {
                L.t += dt / L.dur
                if (L.t >= 1f) { L.moving = false; L.footX = L.toX; L.footY = L.toY; L.lift = 0f } else {
                    val e = L.t * L.t * (3f - 2f * L.t)   // smoothstep
                    L.footX = lerp(L.fromX, L.toX, e)
                    L.footY = lerp(L.fromY, L.toY, e)
                    L.lift = sin(PI.toFloat() * L.t)      // 0..1 подъём
                }
            } else L.lift = 0f
        }

        shadowX = lerp(shadowX, x, dt * 33f)
        shadowY = lerp(shadowY, y, dt * 33f)

        // прозрачность выхода/ухода: у кнопки полностью видим, гаснет по мере ухода вниз ПОД кнопку.
        // hideStart за пределами зоны охраны → при патруле охраны НЕ гаснет.
        val hideStart = cy + radius * 0.34f
        emergeAlpha = clamp((hideY - y) / (hideY - hideStart), 0f, 1f)
    }
}

// ────────────────────────────────────────────────────────────────────────────
//  ГНУЩИЕСЯ ЛАПЫ (mesh-warp) — порт принятого прототипа ?v=19
//  Лапа-спрайт натянута на сетку рёбер вдоль 2-коленной IK-арки, рисуется нативным
//  drawBitmapMesh. Медиальная ось (cx per-row) извлекается из альфы спрайта один раз.
// ────────────────────────────────────────────────────────────────────────────
/** Спрайт лапы (software-битмап для мешинга) + медиальный центр по строкам (px). */
private class LegMesh(val bmp: android.graphics.Bitmap, val cx: FloatArray, val w: Int, val h: Int)

/** Один раз: software-копия + per-row медиальный центр (середина непрозрачных px) с заливкой+сглаживанием. */
private fun buildLegMesh(legBmp: ImageBitmap): LegMesh {
    // ARGB_8888-копия (софт): нужна для getPixels (hardware-битмап не читается) и безопасна для drawBitmapMesh.
    // Всегда копируем → не ссылаемся на Bitmap.Config.HARDWARE (API26, уронил бы lint NewApi на minSdk23).
    val src = legBmp.asAndroidBitmap()
    val sw = src.copy(android.graphics.Bitmap.Config.ARGB_8888, false) ?: src
    val w = sw.width; val h = sw.height
    val cx = FloatArray(h); val has = BooleanArray(h); val row = IntArray(w)
    for (y in 0 until h) {
        sw.getPixels(row, 0, w, 0, y, w, 1)
        var mn = Int.MAX_VALUE; var mx = -1
        for (x in 0 until w) if ((row[x] ushr 24) > 40) { if (x < mn) mn = x; if (x > mx) mx = x }
        if (mx >= 0) { cx[y] = (mn + mx) * 0.5f; has[y] = true }
    }
    var first = -1; var last = -1
    for (y in 0 until h) if (has[y]) { if (first < 0) first = y; last = y }
    if (first < 0) { for (y in 0 until h) cx[y] = w * 0.5f }
    else {
        for (y in 0 until first) cx[y] = cx[first]
        for (y in last + 1 until h) cx[y] = cx[last]
        var prev = cx[first]
        for (y in first..last) { if (has[y]) prev = cx[y] else cx[y] = prev }   // forward-fill gaps
        val tmp = FloatArray(h)
        for (p in 0 until 2) {                                                    // smooth (окно 5, 2 прохода)
            System.arraycopy(cx, 0, tmp, 0, h)
            for (y in first..last) { var s = 0f; var n = 0
                for (k in -2..2) { val z = y + k; if (z in first..last) { s += tmp[z]; n++ } }
                cx[y] = s / n }
        }
    }
    return LegMesh(sw, cx, w, h)
}

// Скретч-буферы (отрисовка однопоточная в drawBehind → без аллокаций/GC на кадр).
private val sPX = FloatArray(MESH_DENSE); private val sPY = FloatArray(MESH_DENSE)
private val sPX2 = FloatArray(MESH_DENSE); private val sPY2 = FloatArray(MESH_DENSE)
private val sCL = FloatArray(MESH_DENSE)
private val sOX = FloatArray(NRIB + 1); private val sOY = FloatArray(NRIB + 1)
private val sNX = FloatArray(NRIB + 1); private val sNY = FloatArray(NRIB + 1)
private val sVerts = FloatArray((NRIB + 1) * 4)
private val sLegPaint = android.graphics.Paint().apply { isFilterBitmap = true; isAntiAlias = true }
private val sBlackFilter =
    android.graphics.PorterDuffColorFilter(android.graphics.Color.BLACK, android.graphics.PorterDuff.Mode.SRC_ATOP)

/** Строит гладкий путь hip→k1→k2→foot в скретч sOX/sOY(+нормали sNX/sNY), NRIB+1 равных по длине точек. */
private fun buildBentPath(
    ax: Float, ay: Float, k1x: Float, k1y: Float, k2x: Float, k2y: Float, bx: Float, by: Float,
) {
    var m = 0
    // плотная выборка 3 сегментов
    val segX = floatArrayOf(ax, k1x, k2x); val segY = floatArrayOf(ay, k1y, k2y)
    val endX = floatArrayOf(k1x, k2x, bx); val endY = floatArrayOf(k1y, k2y, by)
    for (s in 0 until 3) {
        val sx = segX[s]; val sy = segY[s]; val ex = endX[s]; val ey = endY[s]
        val seg = hypot(ex - sx, ey - sy); val n = (seg / 7f).toInt().coerceAtLeast(3)
        var i = 0
        while (i < n && m < MESH_DENSE - 1) { val t = i.toFloat() / n; sPX[m] = sx + (ex - sx) * t; sPY[m] = sy + (ey - sy) * t; m++; i++ }
    }
    if (m < MESH_DENSE) { sPX[m] = bx; sPY[m] = by; m++ }
    for (pass in 0 until LEG_SMOOTH) {                       // сглаживание углов колен
        System.arraycopy(sPX, 0, sPX2, 0, m); System.arraycopy(sPY, 0, sPY2, 0, m)
        for (i in 1 until m - 1) {
            sPX[i] = (sPX2[i - 1] + 2f * sPX2[i] + sPX2[i + 1]) / 4f
            sPY[i] = (sPY2[i - 1] + 2f * sPY2[i] + sPY2[i + 1]) / 4f
        }
    }
    sCL[0] = 0f
    for (i in 1 until m) sCL[i] = sCL[i - 1] + hypot(sPX[i] - sPX[i - 1], sPY[i] - sPY[i - 1])
    val tot = sCL[m - 1].coerceAtLeast(1e-4f)
    for (j in 0..NRIB) {                                     // ресемпл равномерно по длине
        val ss = j.toFloat() / NRIB * tot
        var i = 1; while (i < m - 1 && sCL[i] < ss) i++
        val t = (ss - sCL[i - 1]) / (sCL[i] - sCL[i - 1]).coerceAtLeast(1e-4f)
        sOX[j] = sPX[i - 1] + (sPX[i] - sPX[i - 1]) * t
        sOY[j] = sPY[i - 1] + (sPY[i] - sPY[i - 1]) * t
    }
    for (j in 0..NRIB) {                                     // нормали (касательная +90°)
        val a = (j - 1).coerceAtLeast(0); val b = (j + 1).coerceAtMost(NRIB)
        val tx = sOX[b] - sOX[a]; val ty = sOY[b] - sOY[a]; val l = hypot(tx, ty).coerceAtLeast(1e-4f)
        sNX[j] = -ty / l; sNY[j] = tx / l
    }
}

/**
 * Рисует лапу как ИЗОГНУТУЮ ленту: спрайт натянут на сетку рёбер вдоль 2-коленной арки hip→foot.
 * baseLen = reach*sc (длина внутри ×SLACK). black=true → чёрный силуэт для тени.
 */
private fun DrawScope.drawLegMesh(
    sim: SpiderSim, mesh: LegMesh, ax: Float, ay: Float, bx: Float, by: Float,
    baseLen: Float, alpha: Float, black: Boolean,
) {
    val vx = bx - ax; val vy = by - ay; val D = hypot(vx, vy)
    if (D < 2f) return
    var Lg = baseLen * LEG_SLACK; if (Lg < D * 1.02f) Lg = D * 1.02f
    val hF = mesh.h.toFloat(); val wF = mesh.w.toFloat()
    val sCross = Lg / hF * LEG_WID
    val ux = vx / D; val uy = vy / D
    // полюс колена: наружу-радиально; для ~радиальных (передняя пара вдоль heading) — латерально
    var nx = -uy; var ny = ux
    val mx = (ax + bx) * 0.5f; val my = (ay + by) * 0.5f
    val rx = mx - sim.x; val ry = my - sim.y; val rl = hypot(rx, ry).coerceAtLeast(1e-4f)
    val dr = (nx * rx + ny * ry) / rl
    if (abs(dr) < 0.40f) {
        val ba = sim.headingWorld
        val lx = cos(ba + PI.toFloat() / 2f); val ly = sin(ba + PI.toFloat() / 2f)
        val sgn = if ((ax - sim.x) * lx + (ay - sim.y) * ly >= 0f) 1f else -1f
        if ((nx * lx + ny * ly) * sgn < 0f) { nx = -nx; ny = -ny }
    } else if (dr < 0f) { nx = -nx; ny = -ny }
    val h1 = Lg * KH1; val h2 = Lg * KH2
    val k1x = ax + ux * (D * KF1) + nx * h1; val k1y = ay + uy * (D * KF1) + ny * h1
    val k2x = ax + ux * (D * KF2) + nx * h2; val k2y = ay + uy * (D * KF2) + ny * h2
    buildBentPath(ax, ay, k1x, k1y, k2x, k2y, bx, by)
    for (j in 0..NRIB) {                                     // full-width [0,W] маппинг, медиаль cx на путь
        val yb = ((j.toFloat() / NRIB) * (mesh.h - 1)).toInt().coerceIn(0, mesh.h - 1)
        val cxj = mesh.cx[yb]
        val offL = (0f - cxj) * sCross; val offR = (wF - cxj) * sCross
        val px = sOX[j]; val py = sOY[j]; val pnx = sNX[j]; val pny = sNY[j]
        sVerts[j * 4] = px + pnx * offL;     sVerts[j * 4 + 1] = py + pny * offL
        sVerts[j * 4 + 2] = px + pnx * offR; sVerts[j * 4 + 3] = py + pny * offR
    }
    val paint = sLegPaint
    paint.alpha = (alpha * 255f).toInt().coerceIn(0, 255)
    paint.colorFilter = if (black) sBlackFilter else null
    drawContext.canvas.nativeCanvas.drawBitmapMesh(mesh.bmp, 1, NRIB, sVerts, 0, null, 0, paint)
}

/**
 * Рисует лапа-текстуру так, чтобы её ПИВОТ(бедро)→A и КОНЧИК(стопа)→B (поворот + равномерный масштаб).
 * mirror=true → ЛЕВАЯ лапа: тот же битмап, зеркалим по X (пивот/кончик x→W-1-x, +горизонтальный flip).
 * (Осталось для педипальп — короткие щупальца, изгиб не нужен.)
 */
private fun DrawScope.drawLegSprite(
    bmp: ImageBitmap,
    ax: Float, ay: Float,
    bx: Float, by: Float,
    mirror: Boolean,
    alpha: Float = 1f,
    filter: ColorFilter? = null,
) {
    // ОРИГИНАЛЬНЫЕ пивот(бедро)/кончик(стопа) в px лапа-битмапа (правая ориентация).
    val pvx = LEG_PIV_XF * PAUK_LEG_W
    val pvy = LEG_PIV_YF * PAUK_LEG_H
    val tpx = LEG_TIP_XF * PAUK_LEG_W
    val tpy = LEG_TIP_YF * PAUK_LEG_H
    val dx = tpx - pvx; val dy = tpy - pvy
    val ul = hypot(dx, dy).coerceAtLeast(1e-4f)
    val vx = bx - ax; val vy = by - ay
    val vl = hypot(vx, vy)
    if (vl < 1f) return
    val s = vl / ul
    val angV = atan2(vy, vx)
    // Пивот всегда → A (нулевой вектор). Угол выбираем так, чтобы кончик → B.
    // Без зеркала: направление (dx,dy) после scale(s,s) = atan2(dy,dx); с зеркалом scale(-s,s) → atan2(dy,-dx).
    val angU = if (mirror) atan2(dy, -dx) else atan2(dy, dx)
    val rotDeg = (angV - angU) * 180f / PI.toFloat()
    withTransform({
        translate(ax, ay)
        rotate(rotDeg, pivot = Offset.Zero)
        if (mirror) scale(-s, s, pivot = Offset.Zero) else scale(s, s, pivot = Offset.Zero)
        translate(-pvx, -pvy)   // пивот оригинального битмапа → начало координат
    }) {
        drawImage(bmp, topLeft = Offset.Zero, alpha = alpha, colorFilter = filter)
    }
}

/**
 * The hero connect button — photoreal spider medallion. The spider is a real-time procedural
 * sim (ported from build_final_spider.py): real leg sprite placed hip→foot via matrix, 2D foot
 * planting, alternating tetrapod, OMNIdirectional patrol (body never rotates), idle behaviours,
 * pedipalps, sway, a soft directional drop-shadow. Driven by withFrameNanos → redraws every
 * frame with NO recomposition (frame.value read only inside drawBehind).
 */
@Composable
fun PaukMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier = Modifier,
    backdropMode: Boolean = false,   // true = медальон-кольцо уже на фоне-эскизе → рисуем ТОЛЬКО паука
) {
    val power by animateFloatAsState(
        if (connected) 1f else 0.16f, tween(900, easing = FastOutSlowInEasing), label = "power",
    )
    val live by animateFloatAsState(
        if (connected) 1f else 0f, tween(1000, easing = FastOutSlowInEasing), label = "live",
    )

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val btnScale by animateFloatAsState(
        if (pressed) 0.94f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "medScale",
    )
    val lowRam = rememberIsLowRam()

    val bgP = painterResource(R.drawable.home_medallion_bg)
    val bodyBmp = ImageBitmap.imageResource(R.drawable.pauk_body)
    val legBmp = ImageBitmap.imageResource(R.drawable.pauk_leg)
    val legMesh = remember(legBmp) { buildLegMesh(legBmp) }   // software-копия + медиаль (один раз)
    val webFilter = ColorFilter.colorMatrix(
        ColorMatrix().apply { setToSaturation(0.12f + 0.88f * power) },
    )

    // симуляция + часовой кадр (перерисовка каждый кадр без рекомпозиции)
    val sim = remember { SpiderSim() }
    val frame = remember { mutableStateOf(0L) }
    val connectedLatest = rememberUpdatedState(connected)
    LaunchedEffect(Unit) {
        var last = 0L
        while (true) {
            withFrameNanos { t ->
                val dt = if (last == 0L) 0.016f else ((t - last) / 1e9f).coerceAtMost(0.05f)
                last = t
                sim.pendingDt = dt
                sim.pendingConnected = connectedLatest.value
                frame.value = t
            }
        }
    }

    // Размеры: КНОПКА (медальон-кольцо) увеличена; ПАУК живёт в бОльшем ПОЛЕ без круглого клипа —
    // лапы свободно выходят ЗА кнопку на тёмный фон, паук ходит вокруг и охраняет кнопку.
    // Поле выше, чем шире: передние/задние лапы тянутся вверх/вниз (за кольцо), по бокам — уже.
    val medSize = 272.dp       // кнопка (кольцо+паутина) — было 232
    val btnSize = 240.dp       // тап-зона по центру кольца
    val fieldW = 360.dp        // ширина поля паука (hero/non-backdrop)
    val fieldH = 440.dp        // высота поля (запас для лап вверх/вниз за кнопку)
    // В backdrop-режиме (телефон) поле тянется на ШИРИНУ экрана → паук масштабируется ВМЕСТЕ с
    // запечённым кольцом подложки (кольцо = доля ширины экрана при ContentScale.Crop). Фикс-360dp
    // рассинхронивался с кольцом на узких/широких телефонах.
    val fieldMod = if (backdropMode) Modifier.fillMaxWidth().height(fieldH) else Modifier.size(fieldW, fieldH)

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.then(fieldMod).graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // КНОПКА: чистый медальон (хром-кольцо + зелёная паутина) + внутренние эффекты + затемнение.
        // В backdropMode медальон-кольцо УЖЕ на фоне-эскизе → своё не рисуем, только паук поверх.
        if (!backdropMode) {
            Box(Modifier.size(medSize).clip(CircleShape)) {
                Image(bgP, null, Modifier.size(medSize), contentScale = ContentScale.Fit, colorFilter = webFilter)
                PaukRingEffects(power = power, lowRam = lowRam, modifier = Modifier.size(medSize))
                // dim scrim when powered down
                Box(Modifier.size(medSize).drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.30f)) })
            }
        }

        // ПАУК — отдельный слой БЕЗ клипа, размером с поле → лапы/тело выходят ЗА кольцо-кнопку.
        // ОДИН проход drawBehind, читаем frame.value → кадрит каждый кадр без рекомпозиции.
        Box(
            fieldMod.drawBehind {
                @Suppress("UNUSED_EXPRESSION")
                frame.value
                // прокрутить симуляцию по размеру ПОЛЯ (dt посчитан в withFrameNanos)
                sim.update(sim.pendingDt, size.width, size.height, sim.pendingConnected)
                // спрятан под кнопкой → ничего не рисуем (экономим кадр)
                if (sim.emergeAlpha > 0.004f) {
                    // --- ТЕНЬ (perf-gated): low-RAM = дешёвый контактный блоб; иначе — 1 силуэт-пасс ---
                    if (lowRam) {
                        val sc0 = Offset(sim.shadowX, sim.shadowY + 6f)
                        val rr = sim.sc * BODY_REF * 0.42f
                        drawCircle(
                            brush = Brush.radialGradient(
                                0f to Color.Black.copy(alpha = 0.30f * sim.emergeAlpha),
                                1f to Color.Transparent,
                                center = sc0, radius = rr,
                            ),
                            radius = rr, center = sc0,
                        )
                    } else {
                        drawSpiderSilhouette(sim, bodyBmp, legMesh)
                    }
                    // --- сам паук ---
                    drawSpider(sim, bodyBmp, legMesh, legBmp, live, power)
                }
            },
        )

        Button(
            onClick = onToggle,
            shape = CircleShape,
            interactionSource = interaction,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier.size(btnSize).focusRequester(focusRequester),
            content = {},
        )
    }
}

/** Мягкая направленная drop-тень (1 пасс, не low-RAM): силуэт лап+тела чёрным, смещён + низкая альфа. */
private fun DrawScope.drawSpiderSilhouette(sim: SpiderSim, bodyBmp: ImageBitmap, mesh: LegMesh) {
    val rz = 1f + 0.6f * sim.rise
    val offX = 11f * sim.sc * rz
    val offY = 17f * sim.sc * rz
    val a = 0.34f * sim.emergeAlpha        // тень тоже гаснет при уходе под кнопку
    withTransform({ translate(offX, offY) }) {
        // задние пары под телом
        for (i in 4 until sim.legs.size) drawLegForRise(sim, mesh, sim.legs[i], a, black = true)
        // тело
        drawBodySprite(sim, bodyBmp, a, BLACK_TINT, drawEyes = false, live = 0f)
        // передние пары
        for (i in 0 until 4) drawLegForRise(sim, mesh, sim.legs[i], a, black = true)
    }
}

/** Порядок отрисовки: задние лапы → тело(+глаза) → передние лапы → педипальпы. */
private fun DrawScope.drawSpider(
    sim: SpiderSim, bodyBmp: ImageBitmap, mesh: LegMesh, legBmp: ImageBitmap, live: Float, power: Float,
) {
    val a = sim.emergeAlpha        // общий fade выхода/ухода под кнопку
    // задние пары (idx 4..7) под телом
    for (i in 4 until sim.legs.size) drawLegForRise(sim, mesh, sim.legs[i], a, black = false)
    // тело + глаза
    drawBodySprite(sim, bodyBmp, a, null, drawEyes = true, live = live * power * a)
    // педипальпы (щупают, поверх головы, но под передними лапами) — прямой спрайт (короткие)
    for (p in sim.palps) {
        val hp = sim.hipWPalp(p)
        drawLegSprite(legBmp, hp.x, hp.y, p.footX, p.footY, mirror = p.side < 0, alpha = a)
    }
    // передние пары (idx 0..3) над телом
    for (i in 0 until 4) drawLegForRise(sim, mesh, sim.legs[i], a, black = false)
}

/** Лапа с учётом RISE: при подъёме стопа подтягивается ~0.13·rise к бедру (лапа укорачивается). */
private fun DrawScope.drawLegForRise(
    sim: SpiderSim, mesh: LegMesh, leg: SimLeg, alpha: Float, black: Boolean,
) {
    val a = sim.hipW(leg)
    var bx = leg.footX; var by = leg.footY
    val r = sim.rise
    if (r > 0.01f) { val k = 1f - 0.13f * r; bx = a.x + (bx - a.x) * k; by = a.y + (by - a.y) * k }
    drawLegMesh(sim, mesh, a.x, a.y, bx, by, leg.reach * sim.sc, alpha, black)
}

/** Тело-спрайт: масштаб *(1+0.13·rise), лёгкое «дыхание», амбер-глаза (по live·power). */
private fun DrawScope.drawBodySprite(
    sim: SpiderSim, bodyBmp: ImageBitmap, alpha: Float, filter: ColorFilter?,
    drawEyes: Boolean, live: Float,
) {
    // спрайт тела 176×390; рисуем центрированно, поворот=heading(+sway), масштаб SC·crouch·(1+0.13·rise)
    val bodyScale = sim.sc * (BODY_REF / PAUK_BODY_H) * sim.crouch * (1f + 0.13f * sim.rise)
    val rot = (sim.headingWorld + PI.toFloat() / 2f) * 180f / PI.toFloat()   // тело смотрит по heading (+sway)
    withTransform({
        translate(sim.x, sim.y)
        rotate(rot, pivot = Offset.Zero)
        scale(bodyScale, bodyScale * (1f + sim.breathe), pivot = Offset.Zero)
        translate(-PAUK_BODY_W / 2f, -PAUK_BODY_H / 2f)
    }) {
        drawImage(bodyBmp, topLeft = Offset.Zero, alpha = alpha, colorFilter = filter)
    }
    if (drawEyes && live > 0.01f) {
        // глаза в мировых координатах (после отрисовки тела) — амбер-свечение
        val eyeA = live * (0.45f + 0.35f * (0.5f + 0.5f * sin(sim.bt * 3f)))
        val er = sim.sc * 9f
        // локальные точки глаз в 200px‑теле: ey ≈ -BH*0.32 (в px спрайта) → в 200px системе
        val eyLocalY = -PAUK_BODY_H * 0.32f * (BODY_REF / PAUK_BODY_H)
        for (exf in floatArrayOf(-0.11f, 0.11f)) {
            val exLocalX = exf * PAUK_BODY_W * (BODY_REF / PAUK_BODY_H)
            val ec = sim.toWorld(exLocalX, eyLocalY)
            drawCircle(
                brush = Brush.radialGradient(
                    0f to EYE_COLOR.copy(alpha = eyeA),
                    0.55f to EYE_COLOR.copy(alpha = eyeA * 0.45f),
                    1f to Color.Transparent,
                    center = ec, radius = er,
                ),
                radius = er, center = ec,
            )
        }
    }
}

/** Static inner-rim green glow (power-scaled) + an optional rotating steel shine on the ring.
 *  Both fill the passed medallion size (matchParentSize) so they scale with the ring. */
@Composable
private fun PaukRingEffects(power: Float, lowRam: Boolean, modifier: Modifier) {
    Box(modifier) {
        Box(
            Modifier.matchParentSize().drawBehind {
                val c = Offset(size.width / 2f, size.height / 2f)
                val r = size.minDimension / 2f
                drawCircle(
                    brush = Brush.radialGradient(
                        0.62f to Color.Transparent,
                        0.78f to NeonGreen.copy(alpha = 0.05f + 0.22f * power),
                        0.88f to Color.Transparent,
                        center = c, radius = r,
                    ),
                    radius = r, center = c,
                )
            },
        )
        if (!lowRam) PaukRingShine(power, Modifier.matchParentSize())
    }
}

/** A slow bright arc sweeping around the chrome ring (~10s/rev). Perpetual → non-low-RAM only. */
@Composable
private fun PaukRingShine(power: Float, modifier: Modifier) {
    val ang by rememberInfiniteTransition(label = "ringShine").animateFloat(
        0f, 360f, infiniteRepeatable(tween(10000, easing = LinearEasing), RepeatMode.Restart),
        label = "ringAng",
    )
    Box(
        modifier.drawBehind {
            val stroke = size.minDimension * 0.055f
            val d = size.minDimension - stroke * 1.8f
            rotate(ang) {
                drawArc(
                    brush = Brush.sweepGradient(
                        0.00f to Color.Transparent,
                        0.06f to Color.White.copy(alpha = 0.18f + 0.20f * power),
                        0.12f to Color.Transparent,
                        1.00f to Color.Transparent,
                    ),
                    startAngle = 0f, sweepAngle = 360f, useCenter = false,
                    topLeft = Offset((size.width - d) / 2f, (size.height - d) / 2f),
                    size = Size(d, d),
                    style = Stroke(width = stroke, cap = StrokeCap.Round),
                )
            }
        },
    )
}

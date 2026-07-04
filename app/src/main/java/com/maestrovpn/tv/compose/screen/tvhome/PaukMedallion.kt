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
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.sin

// ============================================================================
//  MaestroVPN — процедурный 2D-паук (порт из build_final_spider.py).
//  Тело = спрайт pauk_body, 8 лап + 2 педипальпы = реальная лапа-текстура pauk_leg,
//  размещённая бедро→стопа матрицей (поворот+масштаб). 2D-плантинг стоп (без скольжения),
//  чередующийся тетрапод, ОМНИнаправленное перемещение (тело НИКОГДА не крутится, heading=вверх),
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
// увеличение паука относительно исходной раскладки (owner: «очень маленький, на кнопке еле
// заметен» → занять почти весь медальон, кончики лап у самого хром-кольца). ⚠️ выше ~2.07
// самая длинная передняя лапа начнёт заходить за круг и срезаться клипом-CircleShape; поэтому
// блуждание ниже урезано пропорционально — крупному пауку негде ходить, он «стоит на кнопке».
private const val SPIDER_SCALE = 2.0f
private val BLACK_TINT = ColorFilter.tint(Color.Black)   // тень: один общий фильтр, не аллоцируем на кадр

// анатомия лап: [fx,fy(доля спрайта тела), угол°, reach(px)] — правая; левая зеркалится.
// fx/fy взяты из LEGDEF (build_final_spider.py); reach = последний столбец LEGDEF.
private val LEGDEF = arrayOf(
    floatArrayOf(0.72f, 0.14f, 32f, 172f),   // I  перед  (самая длинная)
    floatArrayOf(0.84f, 0.25f, 66f, 150f),   // II
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
    val headingWorld: Float get() = -(PI.toFloat() / 2f) + sway   // heading ФИКСИРОВАН вверх (+sway)
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
    private fun gait() {
        if (legs.any { it.moving }) return
        val groupA = intArrayOf(0, 3, 4, 7)
        val groupB = intArrayOf(1, 2, 5, 6)
        fun strain(g: IntArray): Float {
            var s = 0f
            for (i in g) { val L = legs[i]; val r = restFoot(L); s += dist(L.footX, L.footY, r.x, r.y) }
            return s / g.size
        }
        val sA = strain(groupA); val sB = strain(groupB); val th = 20f * sc
        if (sA > th && sA >= sB) for (i in groupA) startSwing(legs[i])
        else if (sB > th) for (i in groupB) startSwing(legs[i])
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
        // масштаб: тело+лапы должны помещаться в кольцо. SPIDER_SCALE делает паука крупным
        // (owner «еле заметен» → почти весь медальон, кончики передних лап у самого хром-кольца).
        // sc задаёт, сколько px = 1 «200px-тело» px. При крупном пауке блужданию почти нет места →
        // leash-и ниже урезаны: паук в основном стоит по центру и перебирает/шевелит лапами.
        sc = (radius * 0.34f) / 172f * SPIDER_SCALE
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

        if (walking) {
            val dx = targetX - x; val dy = targetY - y; val d = hypot(dx, dy)
            if (d < 10f) {
                walking = false; vx = 0f; vy = 0f; idleT = 0f; behaviorReset()
            } else {
                if (pause > 0f) { pause -= dt; vx *= 0.85f; vy *= 0.85f } else {
                    if (Math.random() < dt * 0.4f) pause = rnd(1f, 2f)
                    // ОМНИнаправленно к цели БЕЗ поворота тела
                    val a = atan2(dy, dx); val spd = rnd(38f, 56f) * sc
                    vx = lerp(vx, cos(a) * spd, dt * 2.5f)
                    vy = lerp(vy, sin(a) * spd, dt * 2.5f)
                }
            }
            gait()
        } else {
            idleT += dt
            behaviorUpdate(dt)
            // отключён → паук спокойнее (реже сам пускается в патруль); подключён → живее
            val roam = if (connected) 0.25f else 0.10f
            // изредка сам неспешно пройдётся в ЛЮБУЮ сторону (тело не крутится), в пределах медальона
            if (idleT > rnd(4f, 7f) && Math.random() < dt * roam) {
                idleT = 0f; walking = true
                // крупный паук: короткие микро-переходы у центра (иначе лапы вылезут за кольцо)
                val ta = rnd(0f, (2f * PI).toFloat()); val r = radius * rnd(0.03f, 0.08f)
                var tx = x + cos(ta) * r; var ty = y + sin(ta) * r
                val ddx = tx - cx; val ddy = ty - cy; val dd = hypot(ddx, ddy); val lim = radius * 0.06f
                if (dd > lim) { tx = cx + ddx / dd * lim; ty = cy + ddy / dd * lim }
                targetX = tx; targetY = ty
            }
            if (hypot(vx, vy) < 3f && Math.random() < dt * 0.5f) {
                val L = legs[(Math.random() * 8).toInt()]; if (!L.moving) startSwing(L)
            }
        }

        // физика тела
        x += vx * dt; y += vy * dt
        // мягкий «поводок» к центру, чтобы никогда не выйти из кольца
        run {
            val ddx = x - cx; val ddy = y - cy; val dd = hypot(ddx, ddy); val lim = radius * 0.06f
            if (dd > lim) { x = cx + ddx / dd * lim; y = cy + ddy / dd * lim }
        }
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
    }
}

/**
 * Рисует лапа-текстуру так, чтобы её ПИВОТ(бедро)→A и КОНЧИК(стопа)→B (поворот + равномерный масштаб).
 * mirror=true → ЛЕВАЯ лапа: тот же битмап, зеркалим по X (пивот/кончик x→W-1-x, +горизонтальный flip).
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

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.size(252.dp).graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // green glow hugging the medallion — brightens with power + focus
        Box(
            Modifier.size(252.dp).drawBehind {
                val c = Offset(size.width / 2f, size.height / 2f)
                val r = size.minDimension * 0.5f
                val a = 0.18f + 0.30f * power + if (focused) 0.26f else 0f
                drawCircle(
                    brush = Brush.radialGradient(
                        0.00f to Color.Transparent, 0.64f to Color.Transparent,
                        0.82f to NeonGreen.copy(alpha = a), 1.00f to Color.Transparent,
                        center = c, radius = r * 1.16f,
                    ),
                    radius = r * 1.16f, center = c,
                )
            },
        )

        Box(Modifier.size(232.dp).clip(CircleShape)) {
            // 1) the owner's clean button (chrome ring + green web)
            Image(bgP, null, Modifier.size(232.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)

            // 1b) ring effects (static inner glow + optional shine, gated off low-RAM)
            PaukRingEffects(power = power, lowRam = lowRam, modifier = Modifier.size(232.dp))

            // 2) процедурный паук — ОДИН проход drawBehind, читаем frame.value → кадрит каждый кадр
            Box(
                Modifier.size(232.dp).drawBehind {
                    // читаем часовой кадр, чтобы drawBehind переигрывался каждый кадр
                    @Suppress("UNUSED_EXPRESSION")
                    frame.value
                    // прокрутить симуляцию (dt посчитан в withFrameNanos)
                    sim.update(sim.pendingDt, size.width, size.height, sim.pendingConnected)

                    // --- ТЕНЬ (perf-gated): low-RAM = один дешёвый контактный блоб; иначе — 1 силуэт-пасс ---
                    if (lowRam) {
                        // дешёвая мягкая контактная тень под телом
                        val sc0 = Offset(sim.shadowX, sim.shadowY + 6f)
                        drawCircle(
                            brush = Brush.radialGradient(
                                0f to Color.Black.copy(alpha = 0.30f),
                                1f to Color.Transparent,
                                center = sc0, radius = sim.sc * BODY_REF * 0.42f,
                            ),
                            radius = sim.sc * BODY_REF * 0.42f, center = sc0,
                        )
                    } else {
                        // 1 проход: силуэт паука (лапы+тело) чёрным, со смещением и низкой альфой
                        drawSpiderSilhouette(sim, bodyBmp, legBmp)
                    }

                    // --- сам паук ---
                    drawSpider(sim, bodyBmp, legBmp, live, power)
                },
            )

            // 3) dim scrim when powered down (the spider is drawn over the ring — no overlay)
            Box(Modifier.size(232.dp).drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.30f)) })
        }

        Button(
            onClick = onToggle,
            shape = CircleShape,
            interactionSource = interaction,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier.size(200.dp).focusRequester(focusRequester),
            content = {},
        )
    }
}

/** Мягкая направленная drop-тень (1 пасс, не low-RAM): силуэт лап+тела чёрным, смещён + низкая альфа. */
private fun DrawScope.drawSpiderSilhouette(sim: SpiderSim, bodyBmp: ImageBitmap, legBmp: ImageBitmap) {
    val rz = 1f + 0.6f * sim.rise
    val offX = 11f * sim.sc * rz
    val offY = 17f * sim.sc * rz
    val tint = BLACK_TINT
    val a = 0.34f
    withTransform({ translate(offX, offY) }) {
        // задние пары под телом
        for (i in 4 until sim.legs.size) drawLegForRise(sim, sim.legs[i], legBmp, a, tint)
        // тело
        drawBodySprite(sim, bodyBmp, a, tint, drawEyes = false, live = 0f)
        // передние пары
        for (i in 0 until 4) drawLegForRise(sim, sim.legs[i], legBmp, a, tint)
    }
}

/** Порядок отрисовки: задние лапы → тело(+глаза) → передние лапы → педипальпы. */
private fun DrawScope.drawSpider(
    sim: SpiderSim, bodyBmp: ImageBitmap, legBmp: ImageBitmap, live: Float, power: Float,
) {
    // задние пары (idx 4..7) под телом
    for (i in 4 until sim.legs.size) drawLegForRise(sim, sim.legs[i], legBmp, 1f, null)
    // тело + глаза
    drawBodySprite(sim, bodyBmp, 1f, null, drawEyes = true, live = live * power)
    // педипальпы (щупают, поверх головы, но под передними лапами)
    for (p in sim.palps) {
        val hp = sim.hipWPalp(p)
        drawLegSprite(legBmp, hp.x, hp.y, p.footX, p.footY, mirror = p.side < 0)
    }
    // передние пары (idx 0..3) над телом
    for (i in 0 until 4) drawLegForRise(sim, sim.legs[i], legBmp, 1f, null)
}

/** Лапа с учётом RISE: при подъёме стопа подтягивается ~0.13·rise к бедру (лапа укорачивается). */
private fun DrawScope.drawLegForRise(
    sim: SpiderSim, leg: SimLeg, legBmp: ImageBitmap, alpha: Float, filter: ColorFilter?,
) {
    val a = sim.hipW(leg)
    var bx = leg.footX; var by = leg.footY
    val r = sim.rise
    if (r > 0.01f) { val k = 1f - 0.13f * r; bx = a.x + (bx - a.x) * k; by = a.y + (by - a.y) * k }
    drawLegSprite(legBmp, a.x, a.y, bx, by, mirror = leg.side < 0, alpha = alpha, filter = filter)
}

/** Тело-спрайт: масштаб *(1+0.13·rise), лёгкое «дыхание», амбер-глаза (по live·power). */
private fun DrawScope.drawBodySprite(
    sim: SpiderSim, bodyBmp: ImageBitmap, alpha: Float, filter: ColorFilter?,
    drawEyes: Boolean, live: Float,
) {
    // спрайт тела 176×390; рисуем центрированно, поворот=heading(+sway), масштаб SC·crouch·(1+0.13·rise)
    val bodyScale = sim.sc * (BODY_REF / PAUK_BODY_H) * sim.crouch * (1f + 0.13f * sim.rise)
    val rot = sim.sway * 180f / PI.toFloat()   // тело качается ±~3° (heading фиксирован вверх)
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

/** Static inner-rim green glow (power-scaled) + an optional rotating steel shine on the ring. */
@Composable
private fun PaukRingEffects(power: Float, lowRam: Boolean, modifier: Modifier) {
    Box(
        modifier.drawBehind {
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
    if (!lowRam) PaukRingShine(power)
}

/** A slow bright arc sweeping around the chrome ring (~10s/rev). Perpetual → non-low-RAM only. */
@Composable
private fun PaukRingShine(power: Float) {
    val ang by rememberInfiniteTransition(label = "ringShine").animateFloat(
        0f, 360f, infiniteRepeatable(tween(10000, easing = LinearEasing), RepeatMode.Restart),
        label = "ringAng",
    )
    Box(
        Modifier.size(232.dp).drawBehind {
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

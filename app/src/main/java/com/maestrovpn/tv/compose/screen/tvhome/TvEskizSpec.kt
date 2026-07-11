package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.R

/**
 * Геометрия ТВ-эскиза владельца (vpnon.png / vpnoff.png, арт-пространство 1672×941).
 * Значения снимает и ассеты режет `ops/tv-eskiz-pipeline.py` — эти константы
 * ДОЛЖНЫ совпадать со спекой `ops/tv-eskiz-spec.json` (canonical = OFF-эскиз).
 * ⛔ НЕ подгонять на глаз: правим эскиз → прогоняем пайплайн → сверяем sim → переносим сюда.
 *
 * Правая панель собирается пиксель-в-пиксель АБСОЛЮТНЫМИ позициями: каждый кроп-PNG
 * `tv_ek_*` кладётся на свой арт-y (top = верх кропа = верх рамки − M(14) поля) внутри
 * скролл-зоны. Спейсеры НЕ используются: на эскизе полоса КОНТАКТЫ и кнопка-телефон
 * ПЕРЕКРЫВАЮТСЯ по вертикали (кропы 494..580 и 545..673) — колонка со спейсерами такую
 * геометрию не выложит (кламп до 0 уводил низ на 35-60px вниз и «за экран», 942>941).
 * Z-порядок = порядок в стеке: телефон рисуется ПОВЕРХ фезер-поля КОНТАКТОВ — как в арте.
 */
internal object TvEskizSpec {
    const val ART_W = 1672f
    const val ART_H = 941f

    // правая панель (арт-x): полоса на всю ширину кнопок «Купить/Обновить/телефон»
    const val PANEL_X0 = 706f
    const val PANEL_X1 = 1600f
    val PANEL_W get() = PANEL_X1 - PANEL_X0            // 894

    // 3-колоночные ряды (код/приложения/поделиться, tg/wa/макс)
    const val COL_X0 = 712f
    const val COL_X1 = 1593f
    const val GUT = 10f
    val CELL_W get() = (COL_X1 - COL_X0 - 2 * GUT) / 3f // 287

    /** высота арт-зоны панели (низ ряда tg/wa/макс 882 + воздух до доп-функционала) */
    const val ZONE_H = 900f

    // медальон-кнопка (cx, cy, r) — «off» покрывает и «on» (центр сдвинут слегка)
    const val RING_CX = 370f
    const val RING_CY = 576f
    const val RING_R = 258f

    // ВИДИМОЕ кольцо медальона per-state (замерено по пикселям запечённых фонов:
    // низ яркого штриха OFF=773, ON=830) — для фокус-обводки, чтобы она обнимала
    // именно нарисованное кольцо, а не общую тап-зону, и не касалась статуса.
    const val RING_OFF_CX = 364f
    const val RING_OFF_CY = 556f
    const val RING_OFF_RVIS = 209f   // (773−556) − 8 внутрь штриха
    const val RING_ON_CX = 376f
    const val RING_ON_CY = 596f
    const val RING_ON_RVIS = 226f    // (830−596) − 8

    // Статус-блок под медальоном: контейнер 788..936 (арт-y). OFF — верхний якорь
    // (позиция эскиза, штрих кольца кончается на 773); ON — НИЖНИЙ якорь: кольцо ON
    // толще (штрих до 830), и прижатый к низу компактный блок гарантированно не
    // пересекает ни кольцо, ни нижний край экрана.
    const val STATUS_CX = 373f
    const val STATUS_TOP = 788f
    const val STATUS_BOTTOM = 936f
    const val DOT_R = 11f

    /** элемент панели: кроп + АБСОЛЮТНЫЙ арт-y верха кропа + арт-высота кропа */
    internal class Bar(val res: Int, val top: Float, val h: Float, val focusable: Boolean = true)
    internal class Row3(val res: IntArray, val top: Float, val h: Float)

    // top = frame_y0 − M, h = (frame_y1 − frame_y0) + 2M — из ops/tv-eskiz-spec.json (OFF)
    val BUY = Bar(R.drawable.tv_ek_buy_off, 43f, 121f)
    val ROW_CODE = Row3(
        intArrayOf(R.drawable.tv_ek_code_off, R.drawable.tv_ek_apps_off, R.drawable.tv_ek_share_off),
        178f, 185f,
    )
    val UPDATE = Bar(R.drawable.tv_ek_update_off, 365f, 116f)
    val KONTAKTY = Bar(R.drawable.tv_ek_kontakty_off, 494f, 86f, focusable = false)
    val PHONE = Bar(R.drawable.tv_ek_phone_off, 545f, 128f)
    val HINT = Bar(R.drawable.tv_ek_hint_off, 676f, 82f, focusable = false)
    val ROW_TG = Row3(
        intArrayOf(R.drawable.tv_ek_tg_off, R.drawable.tv_ek_wa_off, R.drawable.tv_ek_max_off),
        730f, 152f,
    )
}

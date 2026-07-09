package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.R

/**
 * Геометрия ТВ-эскиза владельца (vpnon.png / vpnoff.png, арт-пространство 1672×941).
 * Значения снимает и ассеты режет `ops/tv-eskiz-pipeline.py` — эти константы
 * ДОЛЖНЫ совпадать с выводом пайплайна (спека `ops/tv-eskiz-spec.json`).
 * ⛔ НЕ подгонять на глаз: правим эскиз → прогоняем пайплайн → сверяем sim → переносим сюда.
 *
 * Правая панель собирается пиксель-в-пиксель: фон `tv_bg_*` содержит рамку/лого/медальон/
 * пустое дерево панели (кнопки ЗАМАЗАНЫ), а каждая кнопка — отдельный кроп-PNG `tv_ek_*`,
 * который выкладывается в скролл-колонку в тех же пропорциях, что и на эскизе.
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

    const val TOP_PAD = 20f                            // арт-y верха «Купить»-кропа

    // медальон-кнопка (cx, cy, r) — «off» покрывает и «on» (центр сдвинут слегка)
    const val RING_CX = 370f
    const val RING_CY = 576f
    const val RING_R = 258f

    // OFF-статус под медальоном (арт-space)
    const val STATUS_CX = 373f
    const val STATUS_TOP = 788f
    const val DOT_R = 11f

    /** элемент стека правой панели: кроп + арт-высота + арт-спейсер сверху */
    internal class Bar(val res: Int, val h: Float, val spacer: Float, val focusable: Boolean = true)
    internal class Row3(val res: IntArray, val h: Float, val spacer: Float)

    // высоты = пиксельная высота кропов из пайплайна; спейсеры подобраны так, чтобы
    // низ ряда tg/wa/макс лёг на ~904 (запас снизу под ТВ-оверскан).
    val BUY = Bar(R.drawable.tv_ek_buy_off, 121f, 0f)
    val ROW_CODE = Row3(
        intArrayOf(R.drawable.tv_ek_code_off, R.drawable.tv_ek_apps_off, R.drawable.tv_ek_share_off), 185f, 6f,
    )
    val UPDATE = Bar(R.drawable.tv_ek_update_off, 116f, 2f)
    val KONTAKTY = Bar(R.drawable.tv_ek_kontakty_off, 86f, 6f, focusable = false)
    val PHONE = Bar(R.drawable.tv_ek_phone_off, 128f, 0f)
    val HINT = Bar(R.drawable.tv_ek_hint_off, 82f, 0f, focusable = false)
    val ROW_TG = Row3(
        intArrayOf(R.drawable.tv_ek_tg_off, R.drawable.tv_ek_wa_off, R.drawable.tv_ek_max_off), 152f, 0f,
    )
}

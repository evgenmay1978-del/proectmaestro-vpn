package com.maestrovpn.tv.compose.screen.tvhome

import com.maestrovpn.tv.R

/**
 * Геометрия ТВ-хоума v4 «материал мобильной версии» (арт-пространство 1920×1080).
 * Один экран, БЕЗ скролла (owner 2026-07-12). Ассеты режет `ops/tv-mobile-kit.py` —
 * все числа здесь ОБЯЗАНЫ совпадать с константами генератора (мок одобрен owner'ом).
 *
 * Панели заданы INNER-прямоугольниками (x,y,w,h). Канва ассета шире на поля [M]
 * с каждой стороны — в них запечена тень; Kotlin кладёт ассет в (x−M, y−M) размером
 * (w+2M)×(h+2M), контент центрируется в inner-прямоугольнике. Ничего не растягивается:
 * панели пересобраны генератором из телефонных рам (концы native, рельса тайлом).
 */
internal object TvEskizSpec {
    const val ART_W = 1920f
    const val ART_H = 1080f

    /** поля панель-канвы (запечённая тень) — как M в ops/tv-mobile-kit.py */
    const val M = 28f

    // герой-зона = цельный native-кроп телефонного арта (home_backdrop ×0.86 @ 104,26),
    // запечён в tvm_bg_off/on. Числа ниже — только для посадки живых элементов.
    const val HERO_X = 104f
    const val HERO_W = 642f

    // медальон (сфера↔глаз): тап-зона и видимый обод для фокус-кольца
    const val RING_CX = 422f
    const val RING_CY = 600f
    const val RING_R = 190f     // круглая тап-зона
    const val RING_RVIS = 176f  // видимый обод (фокус-обводка)

    // статус под медальоном (центры строк) + аккаунт/триал-бар под ним
    const val STATUS_CX = 422f
    const val STATUS_MAIN_Y = 894f
    const val STATUS_SUB_Y = 940f
    const val DOT_R = 11f

    // правая зона
    const val X0 = 856f
    const val X1 = 1852f

    /** панель: ассет + INNER-прямоугольник (арт-px) */
    internal class P(val res: Int, val x: Float, val y: Float, val w: Float, val h: Float)

    val CTA = P(R.drawable.tvm_cta, X0, 76f, 868f, 100f)
    val GEAR = P(R.drawable.tvm_sq, 1752f, 76f, 100f, 100f)

    // 3 плитки действий (Ввести код / Приложения VPN / Поделиться)
    const val TILE_Y = 208f
    const val TILE_W = 317f
    const val TILE_H = 142f
    const val TILE_STEP = 341f
    val TILE_RES = R.drawable.tvm_tile

    // пара баров (Обновить приложение / Проверить соединение)
    const val BAR2_Y = 374f
    const val BAR2_W = 486f
    const val BAR2_H = 86f
    const val BAR2_X2 = 1366f
    val BAR2_RES = R.drawable.tvm_bar2

    // секции
    const val HDR_CONTACTS_Y = 502f
    val PHONE = P(R.drawable.tvm_phone, X0, 532f, 996f, 84f)
    const val HINT_Y = 644f
    const val MSG_Y = 672f
    const val MSG_H = 72f
    val MSG_RES = R.drawable.tvm_msg
    const val HDR_PROTO_Y = 786f

    // чипы протоколов 4×2 (иконка + титул + подзаголовок; выбранный = tvm_chip_sel)
    const val CHIP_Y = 818f
    const val CHIP_W = 236f
    const val CHIP_H = 80f
    const val CHIP_STEP_X = 254f
    const val CHIP_STEP_Y = 96f
    const val CHIP_COLS = 4
    val CHIP_RES = R.drawable.tvm_chip
    val CHIP_SEL_RES = R.drawable.tvm_chip_sel

    // аккаунт-бар (левая зона, под статусом); при отсутствии подписки — триал-CTA в том же слоте
    val ACCOUNT = P(R.drawable.tvm_account, 112f, 964f, 626f, 66f)
    val TRIAL = P(R.drawable.tvm_trial, 112f, 964f, 626f, 66f)
}

package com.maestrovpn.tv.compose.screen.tvhome

/**
 * Выбор профиля-источника /info для карточки аккаунта.
 *
 * Перерегистрация клиента создаёт второй профиль (дедуп в claim идёт только по токену):
 * старый профиль с мёртвым токеном остаётся первым по userOrder, /info по нему даёт 404 —
 * карточка показывала «Без профиля», хотя VPN работал через выбранный профиль.
 * Поэтому приоритет у выбранного профиля (Settings.selectedProfile), иначе — первый.
 */
internal object AccountProfilePicker {
    /** id выбранного профиля, если он есть в списке; иначе первый id; null для пустого списка. */
    fun pickId(profileIds: List<Long>, selectedProfileId: Long): Long? {
        if (profileIds.isEmpty()) return null
        return profileIds.firstOrNull { it == selectedProfileId } ?: profileIds.first()
    }
}

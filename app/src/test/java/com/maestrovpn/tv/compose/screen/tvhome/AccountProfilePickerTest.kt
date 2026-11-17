package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Регресс-барьер на карточку «Без профиля» (инцидент 17.11.2026, клиент alekseevskaya):
 * после перерегистрации старый профиль с мёртвым токеном оставался первым по userOrder,
 * и /info по нему давал 404. Источник /info должен быть выбранным профилем.
 */
class AccountProfilePickerTest {

    @Test
    fun emptyListIsNull() {
        assertNull(AccountProfilePicker.pickId(emptyList(), 42L))
    }

    @Test
    fun selectedAmongCandidatesWins() {
        assertEquals(30L, AccountProfilePicker.pickId(listOf(10L, 20L, 30L), 30L))
    }

    @Test
    fun selectedAbsentFallsBackToFirst() {
        assertEquals(10L, AccountProfilePicker.pickId(listOf(10L, 20L, 30L), 99L))
    }

    @Test
    fun defaultNoSelectionFallsBackToFirst() {
        assertEquals(10L, AccountProfilePicker.pickId(listOf(10L, 20L, 30L), -1L))
    }

    @Test
    fun selectedFirstIsKept() {
        assertEquals(10L, AccountProfilePicker.pickId(listOf(10L, 30L), 10L))
    }

    @Test
    fun singleCandidateAlwaysPicked() {
        assertEquals(10L, AccountProfilePicker.pickId(listOf(10L), -1L))
    }

    @Test
    fun selectedAnywhereInListIsFound() {
        assertEquals(20L, AccountProfilePicker.pickId(listOf(30L, 20L, 10L), 20L))
    }
}

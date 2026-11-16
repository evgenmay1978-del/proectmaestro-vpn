package com.maestrovpn.tv.vendor

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Регресс-барьер на петлю HTTP 416 (инцидент 16.11.2026, приставка SEI Robotics Dongle G 4K):
 * полный .part не считался готовым, клиент просил Range с конца файла и получал 416.
 */
class ResumePlanTest {

    @Test
    fun fullPartIsReadyNotResume() {
        // ровно размер APK — качать нечего (это и был баг: раньше уходило в Range и 416)
        assertEquals(ResumePlan.Step.READY, ResumePlan.step(146150721L, 146150721L))
    }

    @Test
    fun partialGetsResumed() {
        assertEquals(ResumePlan.Step.RESUME, ResumePlan.step(146150721L, 1000L))
    }

    @Test
    fun emptyPartGetsResumedFromZero() {
        assertEquals(ResumePlan.Step.RESUME, ResumePlan.step(146150721L, 0L))
    }

    @Test
    fun oversizedPartIsDiscarded() {
        assertEquals(ResumePlan.Step.RESTART, ResumePlan.step(146150721L, 146150722L))
    }

    @Test
    fun partialFrom206Appends() {
        assertEquals(ResumePlan.Reply.APPEND, ResumePlan.reply(206, 146150721L, 1000L))
    }

    @Test
    fun fullBodyFrom200Rewrites() {
        assertEquals(ResumePlan.Reply.FULL, ResumePlan.reply(200, 146150721L, 1000L))
    }

    @Test
    fun rangeBeyondEndOnFullPartIsDone() {
        // главный кейс инцидента: сервер ответил 416, файл уже полный → не ошибка
        assertEquals(ResumePlan.Reply.DONE, ResumePlan.reply(416, 146150721L, 146150721L))
    }

    @Test
    fun rangeNotSatisfiableOnPartialIsStillFailure() {
        // 416 при неполном файле — настоящая ошибка, повторять/удалять должен вызывающий код
        assertEquals(ResumePlan.Reply.FAIL, ResumePlan.reply(416, 146150721L, 5000L))
        assertEquals(ResumePlan.Reply.FAIL, ResumePlan.reply(416, 146150721L, 0L))
    }

    @Test
    fun otherCodesFail() {
        assertEquals(ResumePlan.Reply.FAIL, ResumePlan.reply(404, 146150721L, 1000L))
        assertEquals(ResumePlan.Reply.FAIL, ResumePlan.reply(500, 146150721L, 1000L))
    }
}

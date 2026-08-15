package com.maestrovpn.tv.bg

import org.junit.Assert.assertEquals
import org.junit.Test

class WdttStartPolicyTest {
    private val request = WdttCaptchaRequest(
        mode = "wv",
        redirectUri = "https://id.vk.com/captcha",
        sessionToken = "opaque-session",
    )

    @Test fun ordinaryStartUsesABoundedDeadline() {
        val state = WdttStartState.Waiting(startedAtMs = 1_000L, stage = WdttStage.VK_AUTH)

        assertEquals(
            WdttStartDecision.WAIT,
            nextWdttStartDecision(state, childAlive = true, nowMs = 30_999L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
        assertEquals(
            WdttStartDecision.STOP,
            nextWdttStartDecision(state, childAlive = true, nowMs = 31_000L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
    }

    @Test fun captchaPausesOrdinaryDeadlineAndUsesItsOwnDeadline() {
        val state = WdttStartState.CaptchaRequired(
            startedAtMs = 0L,
            requestedAtMs = 29_000L,
            request = request,
        )

        assertEquals(
            WdttStartDecision.WAIT,
            nextWdttStartDecision(state, childAlive = true, nowMs = 40_000L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
        assertEquals(
            WdttStartDecision.STOP,
            nextWdttStartDecision(state, childAlive = true, nowMs = 149_000L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
    }

    @Test fun childExitAlwaysStopsBeforeReady() {
        val state = WdttStartState.Waiting(startedAtMs = 0L, stage = WdttStage.TURN_ALLOCATED)
        assertEquals(
            WdttStartDecision.STOP,
            nextWdttStartDecision(state, childAlive = false, nowMs = 1L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
    }

    @Test fun cancellationFatalFailureAndStoppedStateStop() {
        val states = listOf(
            WdttStartState.Cancelled(startedAtMs = 0L),
            WdttStartState.Failed(startedAtMs = 0L, code = WdttSafeErrorCode.DTLS_FAILED),
            WdttStartState.Stopped(startedAtMs = 0L),
        )

        states.forEach { state ->
            assertEquals(
                WdttStartDecision.STOP,
                nextWdttStartDecision(state, childAlive = true, nowMs = 1L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
            )
        }
    }

    @Test fun readyRequiresALiveChild() {
        val state = WdttStartState.Ready(startedAtMs = 0L)

        assertEquals(
            WdttStartDecision.STARTED,
            nextWdttStartDecision(state, childAlive = true, nowMs = 1L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
        assertEquals(
            WdttStartDecision.STOP,
            nextWdttStartDecision(state, childAlive = false, nowMs = 1L, ordinaryDeadlineMs = 30_000L, captchaDeadlineMs = 120_000L),
        )
    }
}

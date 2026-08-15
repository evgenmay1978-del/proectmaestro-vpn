package com.maestrovpn.tv.compose.screen.dashboard.groups

import com.maestrovpn.tv.bg.WdttPublicState
import com.maestrovpn.tv.bg.WdttSafeErrorCode
import com.maestrovpn.tv.bg.WdttStage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class WdttSelectionPolicyTest {
    @Test fun captchaRequiredWaitsAndNeverSelectsEarly() {
        val decision = decideWdttSelection(
            started = false,
            state = WdttPublicState(WdttStage.CAPTCHA_REQUIRED, captchaPending = true),
        )

        assertEquals(WdttSelectionDecision.WAIT, decision)
        assertFalse(decision == WdttSelectionDecision.SELECT)
    }

    @Test fun onlyReadyChildPermitsOutboundSelection() {
        assertEquals(
            WdttSelectionDecision.SELECT,
            decideWdttSelection(
                started = true,
                state = WdttPublicState(WdttStage.READY),
            ),
        )
        assertEquals(
            WdttSelectionDecision.FAIL,
            decideWdttSelection(
                started = false,
                state = WdttPublicState(WdttStage.FAILED, WdttSafeErrorCode.VK_AUTH_FAILED),
            ),
        )
    }

    @Test fun terminalCodesMapToFixedRussianMessages() {
        assertEquals("VK-туннель: ошибка доверия TLS", wdttSafeRussianMessage(WdttSafeErrorCode.TLS_TRUST_FAILED))
        assertEquals("VK-туннель: авторизация VK не выполнена", wdttSafeRussianMessage(WdttSafeErrorCode.VK_AUTH_FAILED))
        assertEquals("VK-туннель: проверка VK отменена или истекло время", wdttSafeRussianMessage(WdttSafeErrorCode.CAPTCHA_REQUIRED))
        assertEquals("VK-туннель: провайдер недоступен", wdttSafeRussianMessage(WdttSafeErrorCode.PROVIDER_UNAVAILABLE))
    }

    @Test fun watchdogRestartsOnlyAfterReadyChildDies() {
        assertEquals(
            WdttWatchdogAction.HEALTHY,
            nextWdttWatchdogAction(WdttPublicState(WdttStage.READY), childRunning = true),
        )
        assertEquals(
            WdttWatchdogAction.RESTART_ONCE,
            nextWdttWatchdogAction(WdttPublicState(WdttStage.READY), childRunning = false),
        )
        listOf(
            WdttPublicState(WdttStage.STARTING),
            WdttPublicState(WdttStage.CAPTCHA_REQUIRED, captchaPending = true),
            WdttPublicState(WdttStage.FAILED, WdttSafeErrorCode.TURN_ALLOCATE_FAILED),
            WdttPublicState(WdttStage.STOPPED),
        ).forEach { state ->
            assertEquals(WdttWatchdogAction.STOP, nextWdttWatchdogAction(state, childRunning = false))
        }
    }
}

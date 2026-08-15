package com.maestrovpn.tv.bg

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WdttEventParserTest {
    @Test fun parsesEveryTypedStage() {
        WdttStage.entries.forEach { stage ->
            assertEquals(
                WdttEvent.Stage(stage),
                parseWdttEvent("__WDTT_EVENT__|STAGE|{\"stage\":\"${stage.name}\"}"),
            )
        }
    }

    @Test fun parsesKnownFatalErrorWithoutRawDetails() {
        assertEquals(
            WdttEvent.Failure(WdttSafeErrorCode.TLS_TRUST_FAILED, fatal = true),
            parseWdttEvent("__WDTT_EVENT__|ERROR|{\"code\":\"TLS_TRUST_FAILED\",\"fatal\":true}"),
        )
    }

    @Test fun unknownErrorMapsToInternalAndDropsSecretMarker() {
        val event = parseWdttEvent(
            "__WDTT_EVENT__|ERROR|{\"code\":\"TOKEN_secret-marker\",\"fatal\":true}",
        )
        assertEquals(WdttEvent.Failure(WdttSafeErrorCode.INTERNAL, fatal = true), event)
        assertFalse(event.toString().contains("secret-marker"))
    }

    @Test fun parsesAllExactLegacyNativeCaptchaModesAndRedactsTheirStringForm() {
        listOf("auto", "manual", "selected").forEach { mode ->
            val event = parseWdttEvent(
                "CAPTCHA_SOLVE|$mode|https://id.vk.com/captcha?state=secret-marker|session-secret-marker",
            ) as WdttEvent.Captcha

            assertEquals(mode, event.request.mode)
            assertEquals("https://id.vk.com/captcha?state=secret-marker", event.request.redirectUri)
            assertEquals("session-secret-marker", event.request.sessionToken)
            assertFalse(event.toString().contains("secret-marker"))
        }
    }

    @Test fun rejectsMalformedUnknownAndUnsafeInput() {
        val rejected = listOf(
            "READY",
            "__WDTT_EVENT__|READY|workers=18",
            "__WDTT_EVENT__|STAGE|{\"stage\":\"NOT_READY\"}",
            "__WDTT_EVENT__|STAGE|{\"stage\":\"READY\",\"extra\":true}",
            "__WDTT_EVENT__|ERROR|{\"code\":\"TLS_TRUST_FAILED\"}",
            "__WDTT_EVENT__|ERROR|{\"code\":\"TLS_TRUST_FAILED\",\"fatal\":\"true\"}",
            "CAPTCHA_SOLVE|auto|http://id.vk.com/captcha|session",
            "CAPTCHA_SOLVE|wv|https://id.vk.com/captcha|session",
            "CAPTCHA_SOLVE|rjs|https://id.vk.com/captcha|session",
            "CAPTCHA_SOLVE|auto|https://user@id.vk.com/captcha|session",
            "CAPTCHA_SOLVE|auto|https:///missing-host|session",
            "CAPTCHA_SOLVE|auto|https://id.vk.com/captcha|",
            "noise __WDTT_EVENT__|STAGE|{\"stage\":\"READY\"}",
        )

        rejected.forEach { line -> assertNull(line, parseWdttEvent(line)) }
    }

    @Test fun unknownInputCanNeverYieldReady() {
        val candidates = listOf(
            "READY",
            "__WDTT_EVENT__|NOT_READY|{\"stage\":\"READY\"}",
            "prefix__WDTT_EVENT__|STAGE|{\"stage\":\"READY\"}",
            "__WDTT_EVENT__|STAGE|{\"stage\":\"ready\"}",
        )

        assertTrue(candidates.all { parseWdttEvent(it) != WdttEvent.Stage(WdttStage.READY) })
    }
}

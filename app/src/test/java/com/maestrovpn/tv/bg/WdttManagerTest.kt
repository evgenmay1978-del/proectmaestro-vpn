package com.maestrovpn.tv.bg

import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import com.maestrovpn.tv.compose.wdtt.WdttCaptchaResult
import java.io.StringWriter
import java.util.concurrent.CountDownLatch

class WdttManagerTest {
    @Test fun acceptsValidCredentials() {
        assertNotNull(WdttManager.validateCreds(
            "vpn.example.test:56000", listOf("callHash_1"), "password-123456", 18,
            "android-arm64", listOf("123456", "987654"), "audio",
        ))
    }

    @Test fun rejectsInjectionAndInvalidRanges() {
        assertNull(WdttManager.validateCreds(
            "vpn.example.test:70000", listOf("hash\nEVIL=1"), "short", 0,
            "fingerprint", listOf("not-numeric"), "audio;bad",
        ))
    }

    @Test fun readinessRequiresStructuredReadyEvent() {
        assertTrue(WdttManager.isReadyEvent("__WDTT_EVENT__|READY|workers=18"))
        assertFalse(WdttManager.isReadyEvent("READY"))
        assertFalse(WdttManager.isReadyEvent("__WDTT_EVENT__|NOT_READY|reason=test"))
    }

    @Test fun onePersistentWriterCarriesSequentialCaptchaResults() {
        val sink = StringWriter()
        val exchange = WdttCaptchaExchange(WdttCommandWriter(sink))
        val request = WdttCaptchaRequest("auto", "https://id.vk.com/captcha", "session")

        val first = exchange.open(request)
        assertTrue(exchange.submit(first, WdttCaptchaResult.Success("token-one")))
        val second = exchange.open(request)
        assertTrue(exchange.submit(second, WdttCaptchaResult.Timeout))

        assertEquals(
            listOf("CAPTCHA_RESULT|token-one", "CAPTCHA_RESULT|error:timeout"),
            sink.toString().lineSequence().filter(String::isNotEmpty).toList(),
        )
    }

    @Test fun staleCaptchaRequestCannotWrite() {
        val sink = StringWriter()
        val exchange = WdttCaptchaExchange(WdttCommandWriter(sink))
        val request = WdttCaptchaRequest("manual", "https://id.vk.com/captcha", "session")
        val stale = exchange.open(request)
        val current = exchange.open(request)

        assertFalse(exchange.submit(stale, WdttCaptchaResult.Success("stale-token")))
        assertTrue(exchange.submit(current, WdttCaptchaResult.Cancelled))
        assertEquals("CAPTCHA_RESULT|error:cancelled\n", sink.toString())
    }

    @Test fun stopAndCaptchaCommandsAreSerializedAsWholeLines() {
        val sink = StringWriter()
        val writer = WdttCommandWriter(sink)
        val start = CountDownLatch(1)
        val stopThread = Thread { start.await(); writer.writeStop() }
        val captchaThread = Thread { start.await(); writer.writeCaptchaResult(WdttCaptchaResult.Success("token-two")) }

        stopThread.start()
        captchaThread.start()
        start.countDown()
        stopThread.join()
        captchaThread.join()

        assertEquals(
            setOf("STOP", "CAPTCHA_RESULT|token-two"),
            sink.toString().lineSequence().filter(String::isNotEmpty).toSet(),
        )
    }

    @Test fun invalidSuccessTokenNeverReachesChild() {
        val sink = StringWriter()
        val writer = WdttCommandWriter(sink)
        assertFalse(writer.writeCaptchaResult(WdttCaptchaResult.Success("token\nSTOP")))
        assertEquals("", sink.toString())
    }

    @Test fun televisionAndEarlyChildExitFailClosed() {
        assertFalse(canSpawnWdtt(isTelevision = true, sdkInt = 35, hasCreds = true, binaryExists = true))
        assertTrue(canSpawnWdtt(isTelevision = false, sdkInt = 35, hasCreds = true, binaryExists = true))
        assertEquals(
            WdttStartDecision.STOP,
            nextWdttStartDecision(
                WdttStartState.Waiting(0L, WdttStage.VK_AUTH),
                childAlive = false,
                nowMs = 1L,
                ordinaryDeadlineMs = 30_000L,
                captchaDeadlineMs = 120_000L,
            ),
        )
    }
}

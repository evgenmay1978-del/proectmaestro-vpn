package com.maestrovpn.tv.utils

import java.io.File
import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.assertThrows
import org.junit.Test

/**
 * D4 contract: a /sub answer keeps its HTTP status on the PLAIN transport too.
 *
 * The pinned CDN-edge branch already threw SubscriptionHttpException(status), the plain
 * HttpsURLConnection branch threw a bare IOException("subscription HTTP …") — a regression of
 * the incomplete merge-fix 96a96bd8. httpFetchSubscription maps SubscriptionHttpException to
 * SubscriptionFetch.HttpError and catches Throwable as Network, so only the typed exception
 * reaches SubscriptionHealth.report → the «подписка истекла» dialog.
 *
 * This test pins the mapping and the single decision point both branches share (they had already
 * drifted apart once — that is exactly the bug).
 */
class SubscriptionHttpStatusTest {
    @Test
    fun expiredStatusThrowsTypedExceptionWithItsCode() {
        val error = assertThrows(SubscriptionHttpException::class.java) { requireSubscriptionHttpStatus(402) }
        assertEquals(402, error.status)
        assertTrue("SubscriptionHttpException must stay an IOException", error is IOException)
    }

    @Test
    fun everyNon200StatusThrowsTypedExceptionWithItsCode() {
        for (status in listOf(400, 403, 404, 410, 402, 500, 502, 503)) {
            val error = assertThrows(SubscriptionHttpException::class.java) { requireSubscriptionHttpStatus(status) }
            assertEquals(status, error.status)
        }
    }

    @Test
    fun successStatusPasses() {
        requireSubscriptionHttpStatus(200) // must not throw
    }

    @Test
    fun httpClientHasExactlyOnePlaceThatThrowsTheTypedException() {
        val source = appFile("src/main/java/com/maestrovpn/tv/utils/HTTPClient.kt").readText()
        // Both transports (pinned edge + HttpsURLConnection) must go through the shared check;
        // a second direct throw would let them drift apart again.
        assertEquals(
            "exactly one 'throw SubscriptionHttpException(' is allowed (D4)",
            1,
            Regex("throw SubscriptionHttpException\\(").findAll(source).count(),
        )
        assertEquals(2, Regex("requireSubscriptionHttpStatus\\(status\\)").findAll(source).count())
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

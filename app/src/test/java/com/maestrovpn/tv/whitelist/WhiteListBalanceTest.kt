package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.IOException
import java.io.InputStream
import java.net.URL
import java.security.cert.Certificate
import javax.net.ssl.HttpsURLConnection

class WhiteListBalanceTest {
    private val subscription = "https://balance.example:8443/sub/synthetic-token?ignored=yes#ignored"
    private val response = """{"included_remaining_bytes":1250000000,"purchased_remaining_bytes":2500000000,"available_bytes":3750000000,"period_ends_at_unix":1800000000,"primary_access_state":"active","publication_verdict":"PUBLISHABLE"}"""

    @Test
    fun authenticatedGetUsesOnlySameOriginEndpointAndDecodesAllSixFields() {
        lateinit var connection: FakeConnection
        val balance = WhiteListBalanceClient.fetch(subscription) { url ->
            FakeConnection(url, response).also { connection = it }
        }

        assertEquals(
            WhiteListBalance(1_250_000_000, 2_500_000_000, 3_750_000_000, 1_800_000_000, "active", "PUBLISHABLE"),
            balance,
        )
        assertEquals("https://balance.example:8443/account/whitelist-balance", connection.url.toExternalForm())
        assertEquals("Bearer synthetic-token", connection.getRequestProperty("Authorization"))
        assertEquals("GET", connection.requestMethod)
        assertFalse(connection.instanceFollowRedirects)
        assertFalse(connection.useCaches)
        assertEquals("no-store", connection.getRequestProperty("Cache-Control"))
        assertTrue(connection.connectTimeout in 1..15_000)
        assertTrue(connection.readTimeout in 1..15_000)
        assertTrue(connection.streamClosed)
        assertTrue(connection.disconnected)
    }

    @Test
    fun unsafeSubscriptionUrlsNeverOpenAConnection() {
        listOf(
            "http://balance.example/sub/token",
            "https://user:password@balance.example/sub/token",
            "https:///sub/token",
            "https://balance.example/account/token",
            "https://balance.example/sub/",
            "https://balance.example/sub/token/extra",
            "https://balance.example/sub/token%2Fextra",
            "https://balance.example/sub/token%0D%0AX-Other:value",
        ).forEach { url ->
            var opened = false
            assertNull(WhiteListBalanceClient.fetch(url) {
                opened = true
                FakeConnection(it, response)
            })
            assertFalse(opened)
        }
    }

    @Test
    fun redirectsAndAuthorizationOrAvailabilityErrorsAreUnknownNotZero() {
        listOf(301, 302, 307, 308, 401, 403, 404, 503).forEach { status ->
            var calls = 0
            lateinit var connection: FakeConnection
            assertNull(WhiteListBalanceClient.fetch(subscription) {
                calls++
                FakeConnection(it, response, status).also { opened -> connection = opened }
            })
            assertEquals(1, calls)
            assertFalse(connection.instanceFollowRedirects)
            assertEquals(0, connection.inputRequests)
            assertTrue(connection.disconnected)
        }
    }

    @Test
    fun missingMalformedOrNegativeFieldsNeverBecomeZero() {
        val malformed = listOf(
            "{}",
            "not-json",
            response.replace("1250000000", "-1"),
            response.replace("2500000000", "-1"),
            response.replace("3750000000", "-1"),
            response.replace("1800000000", "-1"),
            response.replace("3750000000", "\"3750000000\""),
            response.replace("3750000000", "null"),
            response.replace("\"PUBLISHABLE\"", "123"),
        )
        malformed.forEach { body ->
            assertNull(WhiteListBalanceClient.fetch(subscription) { FakeConnection(it, body) })
        }
    }

    @Test
    fun bodyReadIsBoundedAndClosedEvenWithoutContentLength() {
        lateinit var connection: FakeConnection
        assertNull(WhiteListBalanceClient.fetch(subscription) {
            FakeConnection(it, " ".repeat(20_000)).also { opened -> connection = opened }
        })
        assertEquals(16_385, connection.bytesRead)
        assertTrue(connection.streamClosed)
        assertTrue(connection.disconnected)

        assertNull(WhiteListBalanceClient.fetch(subscription) {
            FakeConnection(it, response, failRead = true).also { opened -> connection = opened }
        })
        assertTrue(connection.disconnected)
    }

    @Test
    fun displayUsesDecimalRussianGbAndDoesNotRoundPositiveBytesToZero() {
        val balance = WhiteListBalanceClient.fetch(subscription) { FakeConnection(it, response) }!!
        assertEquals("CDN: осталось 3,75 ГБ", balance.displayText())
        assertEquals("CDN: осталось 1 ГБ", balance.copy(availableBytes = 1_000_000_000).displayText())
        assertEquals("CDN: осталось < 0,01 ГБ", balance.copy(availableBytes = 1).displayText())
    }

    @Test
    fun disabledIsHiddenPendingIsNotANumberAndExpiredPurchasedBytesAreFrozen() {
        val balance = WhiteListBalanceClient.fetch(subscription) { FakeConnection(it, response) }!!
        val disabled = WhiteListBalanceClient.fetch(subscription) {
            FakeConnection(it, response.replace("PUBLISHABLE", "DISABLED"))
        }!!
        assertEquals(2_500_000_000L, disabled.purchasedRemainingBytes)
        assertNull(disabled.displayText())
        assertEquals("CDN: обновляется", balance.copy(publicationVerdict = "PROJECTION_PENDING").displayText())
        assertEquals("CDN: обновляется", balance.copy(publicationVerdict = "PROJECTION_STALE").displayText())
        assertEquals("CDN: 2,5 ГБ заморожено", balance.copy(
            primaryAccessState = "expired", publicationVerdict = "PRIMARY_EXPIRED",
        ).displayText())
        assertEquals("CDN: 0 ГБ", balance.copy(availableBytes = 0, publicationVerdict = "NO_BALANCE").displayText())
        assertNull(balance.copy(publicationVerdict = "NO_BALANCE").displayText())
        assertNull(balance.copy(publicationVerdict = "FUTURE").displayText())
        assertNull(balance.copy(primaryAccessState = "unknown").displayText())
    }

    private class FakeConnection(
        url: URL,
        body: String,
        private val status: Int = 200,
        private val failRead: Boolean = false,
    ) : HttpsURLConnection(url) {
        private val bytes = body.toByteArray(Charsets.UTF_8)
        var disconnected = false
        var streamClosed = false
        var inputRequests = 0
        private val stream = object : ByteArrayInputStream(bytes) {
            override fun close() {
                streamClosed = true
                super.close()
            }
        }
        val bytesRead: Int get() = bytes.size - stream.available()

        override fun getResponseCode(): Int = status
        override fun getContentLength(): Int = -1
        override fun getInputStream(): InputStream {
            inputRequests++
            if (failRead) throw IOException("synthetic read failure")
            return stream
        }
        override fun connect() = Unit
        override fun disconnect() { disconnected = true }
        override fun usingProxy(): Boolean = false
        override fun getCipherSuite(): String = "synthetic"
        override fun getLocalCertificates(): Array<Certificate>? = null
        override fun getServerCertificates(): Array<Certificate> = emptyArray()
    }
}

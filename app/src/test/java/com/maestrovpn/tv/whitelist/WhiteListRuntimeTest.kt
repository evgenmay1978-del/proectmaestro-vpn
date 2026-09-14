package com.maestrovpn.tv.whitelist

import java.io.ByteArrayInputStream
import java.net.URL
import java.security.cert.Certificate
import javax.net.ssl.HttpsURLConnection
import org.junit.Assert.*
import org.junit.Test

class WhiteListRuntimeTest {
    private fun body(ttl: Int = 5): String = """{"schema_version":1,"issued_at_unix":100,"fresh_until_unix":${100 + ttl},"projection_version":7,"desired_generation":8,"profiles":[${profile()}]}"""
    private fun profile(): String = """{"route_id":"${"a".repeat(64)}","label":"CDN Test","transport_profile_id":"transport-1","transport_release_id":"release-1","compatibility_preset_id":"preset-1","address":"cdn.example.com","port":443,"server_name":"cdn.example.com","host":"cdn.example.com","path":"/transport","client_id":"00000000-0000-4000-8000-000000000001","encryption":"mlkem768x25519plus.native.0rtt.${"A".repeat(1579)}"}"""

    @Test fun leaseStartsBeforeRequestAndCannotBeExtendedByArrival() {
        val runtime = requireNotNull(WhiteListRuntimeClient.parse(body(), 1_000, 2_000))
        assertEquals(6_000L, runtime.deadlineMillis)
        assertTrue(runtime.fresh(5_999))
        assertFalse(runtime.fresh(6_000))
        assertNull(WhiteListRuntimeClient.parse(body(1), 1_000, 2_000))
        assertNull(WhiteListRuntimeClient.parse(body(6), 1_000, 1_000))
        assertNull(WhiteListRuntimeClient.parse(body(0), 1_000, 1_000))
        assertNull(WhiteListRuntimeClient.parse(body(), Long.MAX_VALUE - 1, Long.MAX_VALUE - 1))
    }

    @Test fun rejectsIncompleteChangedOrAmbiguousTransportBatch() {
        listOf(
            body().replace("\"schema_version\":1", "\"schema_version\":2"),
            body().replace("\"port\":443", "\"port\":80"),
            body().replace("\"host\":\"cdn.example.com\"", "\"host\":\"other.example.com\""),
            body().replace("/transport", "/../transport"),
            body().replace("\"desired_generation\":8", "\"desired_generation\":\"8\""),
            body().replace("\"profiles\":[", "\"unknown\":1,\"profiles\":["),
            body().replace(profile(), "${profile()},${profile()}"),
            body().replace("A".repeat(1579), "A".repeat(1578) + "B"),
        ).forEach { assertNull(WhiteListRuntimeClient.parse(it, 0, 1)) }
        val accepted = requireNotNull(WhiteListRuntimeClient.parse(body(), 0, 1))
        assertNotNull(WhiteListRuntimeClient.parse(body().replace("release-1", "release:1").replace("CDN Test", "x".repeat(255)), 0, 1))
        assertNull(WhiteListRuntimeClient.parse(body().replace("CDN Test", "x".repeat(256)), 0, 1))
        assertNull(WhiteListRuntimeClient.parse(body().replace("CDN Test", " CDN Test"), 0, 1))
        assertFalse(accepted.toString().contains("client"))
        assertFalse(accepted.profiles.single().toString().contains("00000000"))
    }

    @Test fun sendsBearerOnlyToSameHttpsOriginWithoutQueryOrRedirects() {
        lateinit var connection: FakeConnection
        val result = WhiteListRuntimeClient.fetch("https://account.example.com:8443/sub/synthetic_token?format=box", { 100L }) {
            connection = FakeConnection(it, 200, body())
            connection
        }
        assertNotNull(result)
        assertEquals("https://account.example.com:8443/account/whitelist-runtime", connection.url.toString())
        assertEquals("Bearer synthetic_token", connection.getRequestProperty("Authorization"))
        assertFalse(connection.instanceFollowRedirects)
        assertFalse(connection.useCaches)
        assertTrue(connection.disconnected)
    }

    @Test fun negativeHttpAndOversizedBodyNeverProduceALease() {
        listOf(301, 302, 401, 403, 404, 409, 503).forEach { status ->
            lateinit var connection: FakeConnection
            assertNull(WhiteListRuntimeClient.fetch("https://account.example.com/sub/synthetic", { 100L }) {
                FakeConnection(it, status, body()).also { fake -> connection = fake }
            })
            assertEquals(0, connection.reads)
            assertTrue(connection.disconnected)
        }
        assertNull(WhiteListRuntimeClient.fetch("https://account.example.com/sub/synthetic", { 100L }) {
            FakeConnection(it, 200, " ".repeat(65_537))
        })
        listOf("http://account.example.com/sub/synthetic", "https://user@account.example.com/sub/synthetic",
            "https://account.example.com/sub/", "https://account.example.com/sub/synthetic#fragment").forEach { url ->
            assertNull(WhiteListRuntimeClient.fetch(url, { 100L }) { error("must reject before opening") })
        }
    }

    @Test fun menuDistinguishesDeniedAccessFromTemporaryOrExpiredResponses() {
        val subscription = "https://account.example.com/sub/synthetic"
        listOf(401, 403, 404).forEach { status ->
            assertEquals(WhiteListRuntimeFetch.Denied, WhiteListRuntimeClient.fetchResult(subscription, { 0L }) {
                FakeConnection(it, status, body())
            })
        }
        assertEquals(WhiteListRuntimeFetch.Unavailable, WhiteListRuntimeClient.fetchResult(subscription, { 0L }) {
            FakeConnection(it, 503, body())
        })
        var ticks = 0
        assertEquals(WhiteListRuntimeFetch.Unavailable, WhiteListRuntimeClient.fetchResult(subscription,
            { if (ticks++ == 0) 0L else 1_500L }) { FakeConnection(it, 200, body(1)) })
        ticks = 0
        assertNull(WhiteListRuntimeClient.fetch(subscription,
            { if (ticks++ == 0) 0L else 1_500L }) { FakeConnection(it, 200, body(1)) })
    }

    @Test fun retainedPhoneMenuLabelsNeverRetainConnectionAuthority() {
        val runtime = requireNotNull(WhiteListRuntimeClient.parse(body(), 1_000, 1_001))
        val ready = whiteListMenuPreview(emptyMap(), sameContext = true, allowed = true, result = WhiteListRuntimeFetch.Ready(runtime))
        assertEquals(mapOf(runtime.profiles.single().tag to "CDN Test"), ready.labels)
        assertEquals(runtime.deadlineMillis, ready.deadlineMillis)
        val unavailable = whiteListMenuPreview(ready.labels, sameContext = true, allowed = true, result = WhiteListRuntimeFetch.Unavailable)
        assertEquals(ready.labels, unavailable.labels)
        assertEquals(0L, unavailable.deadlineMillis)
        for (closed in listOf(
            whiteListMenuPreview(ready.labels, sameContext = true, allowed = true, result = WhiteListRuntimeFetch.Denied),
            whiteListMenuPreview(ready.labels, sameContext = false, allowed = true, result = WhiteListRuntimeFetch.Unavailable),
            whiteListMenuPreview(ready.labels, sameContext = true, allowed = false, result = WhiteListRuntimeFetch.Unavailable),
        )) {
            assertTrue(closed.labels.isEmpty())
            assertEquals(0L, closed.deadlineMillis)
        }
    }

    @Test fun renewalFailureKeepsOnlyTheUnexpiredPermitAndNeverRevivesIt() {
        val initial = requireNotNull(WhiteListRuntimeClient.parse(body(), 1_000, 1_001))
        val route = initial.profiles.single()
        val ready = WhiteListRuntimeFetch.Ready(requireNotNull(WhiteListRuntimeClient.parse(body(), 2_000, 2_001)))
        fun deadline(result: WhiteListRuntimeFetch, now: Long) = whiteListRenewalDeadline(
            initial.deadlineMillis, initial.desiredGeneration, route, result, now)
        assertEquals(initial.deadlineMillis, requireNotNull(deadline(WhiteListRuntimeFetch.Unavailable, 2_000)))
        assertEquals(initial.deadlineMillis, requireNotNull(deadline(WhiteListRuntimeFetch.Unavailable, 5_999)))
        assertNull(deadline(WhiteListRuntimeFetch.Denied, 2_000))
        assertNull(deadline(WhiteListRuntimeFetch.Unavailable, 6_000))
        assertNull(deadline(ready, 6_000))
    }

    @Test fun onlyFreshMatchingMaterialAndGenerationRenewThePermit() {
        val initial = requireNotNull(WhiteListRuntimeClient.parse(body(), 1_000, 1_001))
        val fresh = requireNotNull(WhiteListRuntimeClient.parse(body(), 2_000, 2_001))
        val route = initial.profiles.single()
        fun deadline(runtime: WhiteListRuntime) = whiteListRenewalDeadline(initial.deadlineMillis,
            initial.desiredGeneration, route, WhiteListRuntimeFetch.Ready(runtime), 2_001)
        assertEquals(fresh.deadlineMillis, requireNotNull(deadline(fresh)))
        assertNull(deadline(fresh.copy(deadlineMillis = 2_001)))
        assertNull(deadline(fresh.copy(desiredGeneration = fresh.desiredGeneration + 1)))
        assertNull(deadline(fresh.copy(profiles = emptyList())))
        assertNull(deadline(fresh.copy(profiles = listOf(route.copy(path = "/changed")))))
    }

    private class FakeConnection(url: URL, private val status: Int, private val body: String) : HttpsURLConnection(url) {
        var reads = 0
        var disconnected = false
        override fun getResponseCode() = status
        override fun getInputStream() = ByteArrayInputStream(body.toByteArray()).also { reads++ }
        override fun disconnect() { disconnected = true }
        override fun usingProxy() = false
        override fun connect() = Unit
        override fun getCipherSuite() = "test"
        override fun getLocalCertificates(): Array<Certificate>? = null
        override fun getServerCertificates(): Array<Certificate> = emptyArray()
    }
}

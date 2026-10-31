package com.maestrovpn.tv.aivpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Contract of the automatic AIVPN key pull, checked as a plain JVM test: no Android framework and
 * no network. The transport is injected, so these tests also prove which URL and User-Agent the
 * app would send and that an untrusted subscription URL never opens a connection at all.
 */
class AivpnProvisionerTest {

    private val subscription = "https://wapmixx.ru:8911/sub/tok-ABC_09~+==?device=old&platform=mobile"
    private val userAgent = "SFA/1.0.182 (1018200; sing-box 1.14.0; language ru_RU)"

    // ------------------------------------------------------------------ buildUrl

    @Test
    fun buildUrlKeepsOriginAndPortAndTurnsSubIntoAivpn() {
        assertEquals(
            "https://wapmixx.ru:8911/aivpn/tok-ABC_09~+==?device=d-42",
            AivpnProvisioner.buildUrl(subscription, "d-42"),
        )
        assertEquals(
            "https://panel.example/aivpn/token?device=d-42",
            AivpnProvisioner.buildUrl("https://panel.example/sub/token", "d-42"),
        )
        assertEquals(
            "https://panel.example:8443/aivpn/token?device=d-42",
            AivpnProvisioner.buildUrl("https://panel.example:8443/sub/token", "d-42"),
        )
    }

    @Test
    fun buildUrlUrlEncodesTheDeviceId() {
        assertEquals(
            "https://panel.example/aivpn/token?device=d%2B1%2F2",
            AivpnProvisioner.buildUrl("https://panel.example/sub/token", "d+1/2"),
        )
    }

    @Test
    fun buildUrlRefusesAnythingButAPlainHttpsSubUrl() {
        val unsafe = listOf(
            "http://wapmixx.ru:8911/sub/token",
            "https://user:pass@wapmixx.ru:8911/sub/token",
            "https://wapmixx.ru:8911/aivpn/token",
            "https://wapmixx.ru:8911/cdn-sub/token",
            "https://wapmixx.ru:8911/sub/",
            "https://wapmixx.ru:8911/sub/token/extra",
            "https://wapmixx.ru:8911/sub/token%2Fextra",
            "https://wapmixx.ru:8911/account/token",
            "not a url",
            "",
        )
        unsafe.forEach { assertNull(it, AivpnProvisioner.buildUrl(it, "d-42")) }
        assertNull("without a device the panel returns a login-wide key", AivpnProvisioner.buildUrl(subscription, "   "))
        assertNull(AivpnProvisioner.buildUrl(null, "d-42"))
    }

    @Test
    fun onlyPlainHttpsSubUrlsAreRecognised() {
        assertTrue(AivpnProvisioner.isSubscriptionUrl(subscription))
        assertFalse(AivpnProvisioner.isSubscriptionUrl("http://wapmixx.ru:8911/sub/token"))
        assertFalse(AivpnProvisioner.isSubscriptionUrl("https://wapmixx.ru:8911/cdn-sub/token"))
        assertFalse(AivpnProvisioner.isSubscriptionUrl(null))
    }

    // ------------------------------------------------------------------- mapping

    @Test
    fun successAnswerCarriesTheKey() {
        assertEquals(
            AivpnProvisioner.Result.Ok("aivpn://payload"),
            AivpnProvisioner.interpret(200, """{"key":"aivpn://payload"}"""),
        )
        assertEquals(
            AivpnProvisioner.Result.Ok("aivpn://payload"),
            AivpnProvisioner.interpret(200, """ {"key": "aivpn://payload"} """),
        )
    }

    @Test
    fun successWithoutAKeyIsNotAKey() {
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(200, "not json at all"))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(200, """{"key":""}"""))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(200, """{"error":"nope"}"""))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(200, "{}"))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(200, null))
        assertEquals(
            AivpnProvisioner.Result.Unavailable,
            AivpnProvisioner.interpret(200, """{"key":"https://not-an-aivpn-key"}"""),
        )
    }

    @Test
    fun panelRefusalsMapToShortReasons() {
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.SubscriptionInactive),
            AivpnProvisioner.interpret(402, "subscription expired\n"),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.DeviceLimit),
            AivpnProvisioner.interpret(403, "device limit reached\n"),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.DeviceLimit),
            AivpnProvisioner.interpret(403, """{"error":"device limit reached"}"""),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.Gated),
            AivpnProvisioner.interpret(403, "aivpn unavailable\n"),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.Gated),
            AivpnProvisioner.interpret(403, """{"error":"aivpn unavailable"}"""),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.SubscriptionInactive),
            AivpnProvisioner.interpret(403, """{"error":"subscription expired"}"""),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.Forbidden),
            AivpnProvisioner.interpret(403, "forbidden"),
        )
        assertEquals(
            AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.Forbidden),
            AivpnProvisioner.interpret(403, null),
        )
    }

    @Test
    fun brokenTokenAndPanelErrorsAreNotRefusals() {
        assertEquals(AivpnProvisioner.Result.NotSubscribed, AivpnProvisioner.interpret(404, "not found"))
        assertEquals(AivpnProvisioner.Result.NotSubscribed, AivpnProvisioner.interpret(410, null))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(500, "internal error"))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(302, null))
        assertEquals(AivpnProvisioner.Result.Unavailable, AivpnProvisioner.interpret(-1, null))
    }

    // --------------------------------------------------------------------- fetch

    @Test
    fun fetchAsksTheAivpnEndpointWithTheExactUserAgent() {
        var requestedUrl: String? = null
        var requestedAgent: String? = null
        val result = AivpnProvisioner.fetch(subscription, userAgent, "d-42") { url, agent ->
            requestedUrl = url
            requestedAgent = agent
            AivpnProvisioner.Response(200, """{"key":"aivpn://payload"}""")
        }
        assertEquals(AivpnProvisioner.Result.Ok("aivpn://payload"), result)
        assertEquals("https://wapmixx.ru:8911/aivpn/tok-ABC_09~+==?device=d-42", requestedUrl)
        assertEquals(userAgent, requestedAgent)
    }

    @Test
    fun fetchNeverOpensAConnectionForAnUntrustedUrl() {
        val result = AivpnProvisioner.fetch("http://wapmixx.ru:8911/sub/token", userAgent, "d-42") { _, _ ->
            throw AssertionError("no connection must be opened for a non-https subscription")
        }
        assertEquals(AivpnProvisioner.Result.NotSubscribed, result)
    }

    @Test
    fun fetchMapsARefusalAnswer() {
        val result = AivpnProvisioner.fetch(subscription, userAgent, "d-42") { _, _ ->
            AivpnProvisioner.Response(403, "device limit reached\n")
        }
        assertEquals(AivpnProvisioner.Result.Denied(AivpnProvisioner.Reason.DeviceLimit), result)
    }
}

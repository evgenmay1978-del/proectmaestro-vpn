package com.maestrovpn.tv.utils

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class MaestroSubTest {
    @Test
    fun appendsDeviceAndMobilePlatform() {
        assertEquals(
            "https://example.test/sub/token?device=d-123&platform=mobile",
            MaestroSub.withDeviceMetadata("https://example.test/sub/token", "d-123", "mobile"),
        )
    }

    @Test
    fun preservesExistingDeviceAndAddsTvPlatform() {
        assertEquals(
            "https://example.test/sub/token?device=existing&platform=tv",
            MaestroSub.withDeviceMetadata("https://example.test/sub/token?device=existing", "new", "tv"),
        )
    }

    @Test
    fun isIdempotentAndPreservesCallerPlatform() {
        val url = "https://example.test/sub/token?platform=tv&device=existing"
        assertEquals(url, MaestroSub.withDeviceMetadata(url, "new", "mobile"))
    }

    @Test
    fun insertsMarkersBeforeFragmentAndKeepsExistingQuery() {
        assertEquals(
            "https://example.test/sub/token?app=karing&device=d-123&platform=mobile#section",
            MaestroSub.withDeviceMetadata(
                "https://example.test/sub/token?app=karing#section",
                "d-123",
                "mobile",
            ),
        )
    }

    /**
     * The whitelist fallback keeps the CDN origin first, then dials the pinned whitelist edge by
     * literal address while still carrying the origin's name for SNI/Host.
     */
    @Test
    fun cdnEndpointsKeepOriginThenPinnedWhitelistEdge() {
        val panel = com.maestrovpn.tv.BuildConfig.BACKEND_URL.trimEnd('/')
        val endpoints = MaestroSub.cdnEndpoints("$panel/sub/token?device=d-123", "/cabinet/api/runtime")

        assertEquals("https://cdn-test.wapmixx.ru/cabinet/api/runtime", endpoints.first().url)
        assertNull(endpoints.first().pinnedAddress)
        assertEquals("https://cdn-test.wapmixx.ru/cabinet/api/runtime", endpoints[1].url)
        assertEquals("188.72.103.4", endpoints[1].pinnedAddress)
        assertEquals(1 + MaestroSub.CDN_EDGE_ADDRESSES.size, endpoints.size)
        assertEquals(MaestroSub.CDN_EDGE_ADDRESSES.sorted(), endpoints.drop(1).mapNotNull { it.pinnedAddress }.sorted())
    }

    @Test
    fun cdnEndpointsAreEmptyForForeignSubscriptions() {
        assertTrue(MaestroSub.cdnEndpoints("https://other.example/sub/token").isEmpty())
    }

    @Test
    fun endpointSuffixPrecedesDeviceAndPlatformQuery() {
        assertEquals(
            "https://example.test/sub/token/info?device=d-123&platform=mobile",
            MaestroSub.endpoint(
                "https://example.test/sub/token?device=d-123&platform=mobile",
                "info",
            ),
        )
    }
}

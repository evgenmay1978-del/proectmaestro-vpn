package com.maestrovpn.tv.utils

import org.junit.Assert.assertEquals
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

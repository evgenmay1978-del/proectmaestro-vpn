package com.maestrovpn.tv.utils

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CdnPinnedClientTest {
    private fun bytes(text: String) = text.toByteArray(Charsets.ISO_8859_1)

    @Test
    fun parsesContentLengthBody() {
        val raw = bytes("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 7\r\n\r\n{\"a\":1}")
        val response = CdnPinnedClient.parse(raw, 4096)

        assertEquals(200, response?.status)
        assertEquals("{\"a\":1}", response?.body)
    }

    @Test
    fun parsesChunkedBody() {
        val raw = bytes("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n4\r\n{\"a\":\r\n3\r\n1}\n\r\n0\r\n\r\n")
        val response = CdnPinnedClient.parse(raw, 4096)

        assertEquals(200, response?.status)
        assertEquals("{\"a\":1}\n", response?.body)
    }

    @Test
    fun reportsErrorStatus() {
        val raw = bytes("HTTP/1.1 401 Unauthorized\r\nContent-Length: 22\r\n\r\n{\"error\":\"unauthorized\"}")
        val response = CdnPinnedClient.parse(raw, 4096)

        assertEquals(401, response?.status)
    }

    @Test
    fun rejectsIncompleteHeadersAndOversizedBodies() {
        assertNull(CdnPinnedClient.parse(bytes("HTTP/1.1 200 OK\r\n"), 4096))
        val oversized = bytes("HTTP/1.1 200 OK\r\nContent-Length: 99\r\n\r\n" + "x".repeat(99))
        assertNull(CdnPinnedClient.parse(oversized, 16))
    }
}

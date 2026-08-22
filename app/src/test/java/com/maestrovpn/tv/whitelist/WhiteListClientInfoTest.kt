package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WhiteListClientInfoTest {
    @Test
    fun absentFieldPreservesExistingBehavior() {
        assertNull(
            WhiteListClientInfoParser.parseInfoResponse(
                """{"login":"alice","future":{"ignored":true}}""",
                isTelevision = false,
            ),
        )
    }

    @Test
    fun activeMobileProjectionIsBoundedAndRuntimeEligible() {
        val model = WhiteListClientInfoParser.parseInfoResponse(
            """
            {
              "white_list": {
                "state": "ACTIVE",
                "transport_profile_id": "profile-a",
                "transport_release_id": "release-a",
                "preset": "MAESTRO_ADVANCED",
                "billing_state": "SHADOW",
                "usage_bytes": 1048576,
                "remaining_limit_bytes": 2097152,
                "suspension_reason": "",
                "edge_ids": ["edge-a", "edge-b"],
                "heartbeat_enabled": true,
                "future_field": "ignored"
              }
            }
            """.trimIndent(),
            isTelevision = false,
        )

        assertEquals(WhiteListState.ACTIVE, model?.state)
        assertEquals(listOf("edge-a", "edge-b"), model?.edgeIds)
        assertTrue(model?.runtimeEligible == true)
    }

    @Test
    fun televisionReturnsBeforeParsing() {
        assertNull(
            WhiteListClientInfoParser.parseInfoResponse(
                raw = "not-json-and-must-not-be-parsed",
                isTelevision = true,
            ),
        )
    }

    @Test
    fun malformedAndUnsafeValuesFailClosed() {
        val invalid = listOf(
            """{"white_list":{"state":"ACTIVE","usage_bytes":-1}}""",
            """{"white_list":{"state":"ACTIVE","edge_ids":["edge-a","edge-a"]}}""",
            """{"white_list":{"state":"ACTIVE","transport_profile_id":"bad/token"}}""",
            """{"white_list":{"state":"ACTIVE","suspension_reason":"line
break"}}""",
        )
        invalid.forEach {
            assertNull(WhiteListClientInfoParser.parseInfoResponse(it, isTelevision = false))
        }
    }

    @Test
    fun unknownStateCanDisplayButCannotActivateRuntime() {
        val model = WhiteListClientInfoParser.parseInfoResponse(
            """{"white_list":{"state":"FUTURE","heartbeat_enabled":true,"edge_ids":["edge-a"]}}""",
            isTelevision = false,
        )
        assertEquals(WhiteListState.UNKNOWN, model?.state)
        assertFalse(model?.runtimeEligible == true)
    }
}

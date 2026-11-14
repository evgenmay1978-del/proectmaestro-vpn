package com.maestrovpn.tv.whitelist

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class SubXhttpPlanTest {
    private fun config(vararg outbounds: String) = """{"outbounds":[${outbounds.joinToString(",")}]}"""

    private fun socks(tag: String, port: Int) =
        """{"type":"socks","tag":"$tag","server":"127.0.0.1","server_port":$port}"""

    @Test
    fun profilePortsKeepOnlyLoopbackSocksInsideShimRange() {
        val ports = SubXhttpPlan.profileXhttpPorts(
            config(
                socks("DE node", 20801),
                socks("EE node", 20802),
                socks("olcrtc", 8808),
                socks("wl-1", 41234),
                """{"type":"socks","tag":"remote","server":"10.0.0.1","server_port":20803}""",
                """{"type":"vless","tag":"direct-node","server":"1.2.3.4","server_port":443}""",
            ),
        )
        assertEquals(mapOf("DE node" to 20801, "EE node" to 20802), ports)
    }

    @Test
    fun decideServesProfilePortWhenManifestPortIsStale() {
        // The manifest still remembers 20809, the reloaded profile renumbered the node to 20808.
        val decision = SubXhttpPlan.decide("UK node", 20809, mapOf("UK node" to 20808))
        assertEquals(SubXhttpPlan.Decision.Serve(20808), decision)
    }

    @Test
    fun decideRefusesManifestNodeMissingFromProfile() {
        assertEquals(
            SubXhttpPlan.Decision.Refuse("node is not in the engine profile"),
            SubXhttpPlan.decide("DE node", 20801, emptyMap()),
        )
    }

    @Test
    fun decideRefusesProfileNodeMissingFromManifest() {
        // Used to be treated as a regular node and routed into a closed loopback port.
        val decision = SubXhttpPlan.decide("PL node", null, mapOf("PL node" to 20803))
        assertTrue(decision is SubXhttpPlan.Decision.Refuse)
    }

    @Test
    fun decideLeavesRegularNodesAlone() {
        assertEquals(SubXhttpPlan.Decision.Regular, SubXhttpPlan.decide("JFL node", null, emptyMap()))
        assertEquals(SubXhttpPlan.Decision.Regular, SubXhttpPlan.decide(null, null, mapOf("x" to 20801)))
    }

    @Test
    fun profilePortsDegradeToEmptyOnBrokenJson() {
        assertEquals(emptyMap<String, Int>(), SubXhttpPlan.profileXhttpPorts("{not json"))
    }
}

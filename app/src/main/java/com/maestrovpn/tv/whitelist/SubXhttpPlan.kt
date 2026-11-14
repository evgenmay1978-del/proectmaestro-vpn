package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Pure decision logic of the xhttp runner, free of Android APIs so it can be unit-tested on a
 * plain JVM (locally with kotlinc, and in CI without an emulator).
 *
 * The shim emits the subscription's xhttp nodes in two places: a loopback SOCKS outbound in the
 * sing-box profile (`127.0.0.1:<port>`, tag = node label) and the runner parameters in the
 * manifest (`?format=xray`). The two are refreshed independently, so after a subscription reload
 * their port numbers can disagree. The engine always follows the profile — therefore the runner
 * must listen on the PROFILE port, whatever the manifest still remembers.
 */
internal object SubXhttpPlan {
    /** Port range the shim assigns to its loopback xhttp SOCKS outbounds (20801 + index). */
    const val PORT_MIN = 20801
    const val PORT_MAX = 20999

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * tag -> server_port of every loopback SOCKS outbound that belongs to the shim. Only ports
     * from the shim's own range are ours: the white-list runner binds an ephemeral port and
     * olcRTC keeps 8808, neither may ever be mistaken for an xhttp node (that mistake would make
     * a working node look like an orphan and block its selection).
     */
    fun profileXhttpPorts(
        configJson: String,
        minPort: Int = PORT_MIN,
        maxPort: Int = PORT_MAX,
    ): Map<String, Int> {
        val root = runCatching { json.parseToJsonElement(configJson) as? JsonObject }.getOrNull()
            ?: return emptyMap()
        val outbounds = root["outbounds"] as? JsonArray ?: return emptyMap()
        val ports = LinkedHashMap<String, Int>()
        for (element in outbounds) {
            val obj = element as? JsonObject ?: continue
            if ((obj["type"] as? JsonPrimitive)?.content != "socks") continue
            if ((obj["server"] as? JsonPrimitive)?.content != "127.0.0.1") continue
            val tag = (obj["tag"] as? JsonPrimitive)?.content?.takeIf { it.isNotEmpty() } ?: continue
            val port = (obj["server_port"] as? JsonPrimitive)?.content?.toIntOrNull() ?: continue
            if (port in minPort..maxPort) ports[tag] = port
        }
        return ports
    }

    /** What the session must do with a tapped tag. */
    sealed class Decision {
        /** Our node, present in the live profile: run (or reuse) the runner on exactly this port. */
        data class Serve(val port: Int) : Decision()

        /** Our node that cannot be served: refuse the pick and keep the previous route. */
        data class Refuse(val reason: String) : Decision()

        /** Not ours — the regular libbox selector handles it. */
        object Regular : Decision()
    }

    /**
     * @param manifestPort port from the runner manifest, null when the manifest has no such node.
     * @param profilePorts live profile ports ([profileXhttpPorts] applied to the engine config).
     *
     * Both sides must agree that the tag is ours:
     *  - present in both  -> [Decision.Serve] on the PROFILE port (the engine routes there);
     *  - manifest only    -> refuse: the engine has no outbound for it, selecting it would point
     *                        the tunnel at a closed loopback port;
     *  - profile only     -> refuse: there are no runner parameters, and treating it as a regular
     *                        node (the old behaviour) parked the phone on a port nobody listens on;
     *  - neither          -> regular node, not ours.
     */
    fun decide(tag: String?, manifestPort: Int?, profilePorts: Map<String, Int>): Decision = when {
        tag.isNullOrEmpty() -> Decision.Regular
        manifestPort != null && profilePorts.containsKey(tag) -> Decision.Serve(profilePorts.getValue(tag))
        manifestPort != null -> Decision.Refuse("node is not in the engine profile")
        profilePorts.containsKey(tag) -> Decision.Refuse("node is not in the runner manifest")
        else -> Decision.Regular
    }
}

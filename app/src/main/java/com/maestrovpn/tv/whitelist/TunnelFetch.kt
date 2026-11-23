package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.ServerSocket
import java.net.URL
import javax.net.ssl.HttpsURLConnection

/**
 * Tunnel-fetch: let the subscription (/sub) and balance fetches ride the LIVE tunnel when the
 * direct path to the panel is blocked (TLS to wapmixx.ru hangs from Russian networks), without
 * moving the app itself into the tunnel.
 *
 * Why the fetch does not go through the tunnel today: the app's own UID is ALWAYS kept outside
 * it (PackagePolicy — incident 21.10.2026: a dead config cut claim/sub/OTA off and the user had
 * to force-stop), so HTTPClient talks to the panel on the system network; and even traffic that
 * did reach the box would leave via the panelDirect rule (subgen.go:495: wapmixx.ru → direct).
 *
 * The fix is three small pieces, all in the app (no panel change — a panel change would not
 * reach live clients through an app OTA):
 *  1. [ensure] adds a loopback `mixed` inbound to the applied box config and a route rule that
 *     sends only that inbound's wapmixx.ru traffic to the `select` outbound, placed directly
 *     above panelDirect. Everything else keeps the direct recovery path.
 *  2. [ensureDns] routes the same inbound's wapmixx.ru A/AAAA lookups to the tunnel DoT
 *     (`google`): the panel's own DNS rule sends the panel host to the `local` resolver when
 *     DNSFakeIP is on, and a poisoned local answer would sink the fetch even through the
 *     tunnel. Only the mixed inbound's queries are affected.
 *  3. The fetchers (HTTPClient, WhiteListBalanceClient) try the loopback proxy FIRST when
 *     [isAvailable], and fall back to the direct chain when the proxy is unreachable — so a
 *     dead tunnel costs one instant refused connection, never a stalled refresh.
 *
 * Safety: [ensure]/[ensureDns] are pure string rewrites in the TunIpv6/TunStack/Sniffing
 * pattern — they return the input unchanged when there is nothing to do, and BoxService only
 * applies a candidate the engine itself accepts (Libbox.checkConfig). A rejected rewrite never
 * reaches the tunnel. The mixed inbound listens on 127.0.0.1 only; nothing is exposed.
 */
internal object TunnelFetch {
    /** Tag of the loopback mixed inbound the config patch adds. */
    const val TAG = "tunnel-fetch"

    /** Panel host the inbound-scoped rules cover (mirrors panelDomains in subgen.go). */
    const val PANEL_HOST = "wapmixx.ru"

    /**
     * Bound for ONE proxy attempt (connect AND read each). A healthy tunnel answers in ~1 s;
     * a dead exit stalls — the attempt must fail fast enough to leave budget for the direct
     * and CDN attempts (see subscriptionWaitMs in HTTPClient).
     */
    const val PROXY_TIMEOUT_MS = 3_000

    /**
     * Loopback port of the mixed inbound in the LIVE box config, or 0 when no tunnel is up
     * (or the applied config could not carry the inbound). Set by [ensure] on every apply,
     * cleared by [clear] when the service stops. Volatile: read from the update workers and
     * the balance fetch, written by the box service thread.
     */
    @Volatile
    var proxyPort: Int = 0
        internal set

    /** True while the live box config carries the mixed inbound. */
    val isAvailable: Boolean get() = proxyPort != 0

    /** The HTTP proxy the live tunnel offers on loopback. Only call when [isAvailable]. */
    fun proxy(): Proxy = Proxy(Proxy.Type.HTTP, InetSocketAddress("127.0.0.1", proxyPort))

    /** Open an HTTPS connection to [url] through the live tunnel. Only call when [isAvailable]. */
    fun openConnection(url: String): HttpsURLConnection =
        URL(url).openConnection(proxy()) as HttpsURLConnection

    /** The tunnel is gone: never try the (now stale) loopback port again. */
    fun clear() {
        proxyPort = 0
    }

    /**
     * Add the loopback mixed inbound + the inbound-scoped route rule for the panel host to a
     * box config, and remember the port for the fetchers. Returns the input unchanged (same
     * instance) when there is nothing to do: unparseable JSON, no `inbounds`/`route`, no
     * `select` outbound to route through, or the inbound already present (idempotent — the
     * port is then re-read from the config). A config the engine refuses is the CALLER's
     * problem: it validates with Libbox.checkConfig and keeps the original (see
     * BoxService.applySelectedContent).
     */
    fun ensure(config: String): String {
        proxyPort = 0
        val root = runCatching { Json.parseToJsonElement(config) as? JsonObject }.getOrNull() ?: return config
        val inbounds = root["inbounds"] as? JsonArray ?: return config
        val existing = inbounds.firstOrNull { element ->
            val obj = element as? JsonObject ?: return@firstOrNull false
            (obj["type"] as? JsonPrimitive)?.content == "mixed" &&
                (obj["tag"] as? JsonPrimitive)?.content == TAG
        } as? JsonObject
        if (existing != null) {
            // Idempotent re-apply: keep the port the config already carries.
            (existing["listen_port"] as? JsonPrimitive)?.content?.toIntOrNull()
                ?.takeIf { it in 1..65_535 }?.let { proxyPort = it }
            return config
        }
        val outbounds = root["outbounds"] as? JsonArray ?: return config
        val hasSelect = outbounds.any { element ->
            val obj = element as? JsonObject ?: return@any false
            (obj["tag"] as? JsonPrimitive)?.content == "select"
        }
        if (!hasSelect) return config
        val route = root["route"] as? JsonObject ?: return config
        val rules = route["rules"] as? JsonArray ?: return config
        val port = allocatePort() ?: return config
        val inbound = buildJsonObject {
            put("type", JsonPrimitive("mixed"))
            put("tag", JsonPrimitive(TAG))
            put("listen", JsonPrimitive("127.0.0.1"))
            put("listen_port", JsonPrimitive(port))
        }
        val rule = buildJsonObject {
            put("inbound", JsonArray(listOf(JsonPrimitive(TAG))))
            put("domain_suffix", JsonArray(listOf(JsonPrimitive(PANEL_HOST))))
            put("action", JsonPrimitive("route"))
            put("outbound", JsonPrimitive("select"))
        }
        val list = rules.toMutableList()
        var insertAt = 0
        while (insertAt < list.size) {
            val obj = list[insertAt] as? JsonObject ?: break
            val action = (obj["action"] as? JsonPrimitive)?.content
            if (action == "sniff" || action == "hijack-dns") insertAt++ else break
        }
        // Position 2 in the panel config, directly above panelDirect: route rules are
        // first-match, and this rule only matches this inbound's panel-host traffic, so no
        // other traffic is affected by the early position.
        list.add(insertAt, rule)
        proxyPort = port
        val patchedRoute = JsonObject(route + ("rules" to JsonArray(list)))
        return JsonObject(root + ("inbounds" to JsonArray(inbounds + inbound)) + ("route" to patchedRoute)).toString()
    }

    /**
     * Route the mixed inbound's wapmixx.ru A/AAAA lookups to the tunnel DoT (`google`), ahead
     * of the panel's own panelDomains → `local` rule. Only that inbound's queries are
     * affected — every other lookup keeps the panel's recovery design (a broken tunnel must
     * not take the direct panel path with it). Returns the input unchanged when there is no
     * `google` server, no DNS rules, or the rule is already there.
     */
    fun ensureDns(config: String): String {
        val root = runCatching { Json.parseToJsonElement(config) as? JsonObject }.getOrNull() ?: return config
        val dns = root["dns"] as? JsonObject ?: return config
        val servers = dns["servers"] as? JsonArray ?: return config
        val hasGoogle = servers.any { element ->
            val obj = element as? JsonObject ?: return@any false
            (obj["tag"] as? JsonPrimitive)?.content == "google"
        }
        if (!hasGoogle) return config
        val rules = dns["rules"] as? JsonArray ?: return config
        val already = rules.any { element ->
            val obj = element as? JsonObject ?: return@any false
            val inbound = (obj["inbound"] as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.content } ?: emptyList()
            val server = (obj["server"] as? JsonPrimitive)?.content
            TAG in inbound && server == "google"
        }
        if (already) return config
        val rule = buildJsonObject {
            put("inbound", JsonArray(listOf(JsonPrimitive(TAG))))
            put("domain_suffix", JsonArray(listOf(JsonPrimitive(PANEL_HOST))))
            put("query_type", JsonArray(listOf(JsonPrimitive("A"), JsonPrimitive("AAAA"))))
            put("action", JsonPrimitive("route"))
            put("server", JsonPrimitive("google"))
        }
        val patchedDns = JsonObject(dns + ("rules" to JsonArray(listOf(rule) + rules)))
        return JsonObject(root + ("dns" to patchedDns)).toString()
    }

    /**
     * A free loopback port, reserved the same way WhiteListSession picks runner ports: bind
     * port 0 and read what the OS gave. The box binds it moments later in the same apply, so
     * the race window is negligible — and a bind failure can only come from another process
     * grabbing that exact port in between, which the next apply repairs with a fresh port.
     */
    private fun allocatePort(): Int? = runCatching {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
    }.getOrNull()?.takeIf { it in 1..65_535 }
}

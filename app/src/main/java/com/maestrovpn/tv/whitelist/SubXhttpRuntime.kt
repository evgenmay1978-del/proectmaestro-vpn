package com.maestrovpn.tv.whitelist

import android.os.SystemClock
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.utils.SubscriptionFetch
import com.maestrovpn.tv.utils.httpFetchSubscription
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.io.File
import java.io.InputStream
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/**
 * xhttp-узлы внешней подписки (Аконит), которые движок sing-box воспроизвести не может.
 *
 * Ядро приложения — sing-box, xhttp-транспорт (splithttp) он не умеет. Поэтому такие узлы
 * приезжают из шима двумя частями:
 *  1) в sing-box конфиге — socks-outbound на 127.0.0.1:<port> с тегом "xhttp:<slug>" (обычные
 *     узлы списка, ядро просто проксирует в локальный SOCKS);
 *  2) в манифесте (этот файл) — настоящие параметры узла (vless reality+xhttp) для дочернего
 *     процесса официального Xray, который поднимает этот локальный SOCKS.
 *
 * Манифест тянется с того же доверенного хоста подписки (?format=xray) и кэшируется на диск.
 * Секретов в логах не печатаем: toString() узла не содержит ни uuid, ни адреса.
 */
internal data class SubXhttpNode(
    val tag: String,
    val label: String,
    val socksPort: Int,
    val address: String,
    val outbound: JsonObject,
) {
    override fun toString(): String = "SubXhttpNode(tag=$tag)"
}

internal object SubXhttpRuntime {
    const val TAG_PREFIX = "xhttp:"

    private const val FILE_NAME = "subxhttp-nodes.json"
    private const val MAX_BYTES = 256 * 1024
    private const val IP_TTL_MS = 10 * 60 * 1000L
    private const val RESOLVE_TIMEOUT_MS = 1_500L
    private const val RESOLVE_BUDGET_MS = 4_000L
    private const val DELAY_TARGET_HOST = "api.ipify.org"
    private const val DELAY_TARGET_PORT = 443
    private const val DELAY_TTL_MS = 5 * 60 * 1000L
    private const val RETRY_TTL_MS = 30 * 1000L
    private const val IDLE_PROBE_TIMEOUT_MS = 2_000

    /**
     * TLS handshake budget of the latency probe. The probe is a real TLS handshake to the node
     * entry with the node's own SNI: a path that answers the TCP handshake but never passes a
     * byte (measured on the German/Estonian/Polish Akonit entries, 14.11.2026) times out here.
     */
    private const val PROBE_HANDSHAKE_TIMEOUT_MS = 4_000
    private const val PROBE_BATCH = 4

    private val json = Json { ignoreUnknownKeys = true }
    private val resolver = Executors.newCachedThreadPool { runnable ->
        Thread(runnable, "subxhttp-resolve").apply { isDaemon = true }
    }
    private val probePool = Executors.newFixedThreadPool(PROBE_BATCH) { runnable ->
        Thread(runnable, "subxhttp-probe").apply { isDaemon = true }
    }

    /**
     * Certificate chain is not verified on purpose: the probe only asks whether the entry still
     * completes a TLS handshake on this network path, and a Reality entry answers with the
     * certificate of its own fallback host, which is not the name we dial.
     */
    private val probeTls: SSLContext = run {
        val trustAll = arrayOf<TrustManager>(object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = Unit

            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = Unit

            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        })
        SSLContext.getInstance("TLS").apply { init(null, trustAll, SecureRandom()) }
    }

    @Volatile private var cached: List<SubXhttpNode>? = null
    @Volatile private var resolvedAt = 0L
    @Volatile private var resolvedIps: List<String> = emptyList()
    /** Node IPs already exempted from the tun by the last [patchDirectRules] insertion. */
    @Volatile private var appliedIps: Set<String> = emptySet()

    /**
     * tag -> loopback SOCKS port of the engine profile as of the last config build. The session
     * starts the runner on THIS port (the engine follows the profile, not our manifest), so a
     * subscription reload that renumbered the nodes can never leave the tunnel on a dead port.
     */
    @Volatile private var liveProfilePorts: Map<String, Int> = emptyMap()

    /** Live profile ports of the xhttp nodes; empty until the engine config was built once. */
    fun profilePorts(): Map<String, Int> = liveProfilePorts

    /** What the session must do with this tag: serve it, refuse it, or leave it to libbox. */
    fun decisionFor(tag: String?): SubXhttpPlan.Decision {
        if (tag.isNullOrEmpty()) return SubXhttpPlan.Decision.Regular
        return SubXhttpPlan.decide(tag, node(tag)?.socksPort, liveProfilePorts)
    }

    /** True when the running engine already has a DIRECT rule covering this entry IP. */
    fun ruleApplied(ip: String?): Boolean = ip != null && ip in appliedIps

    /**
     * The shim owns the naming: a node tag is either the technical "xhttp:<slug>" or the panel's
     * human readable label ("🇫🇮 ⚡️Вьюга - Финляндия"). Both are ours, everything else is not.
     */
    fun isSubXhttpTag(tag: String?): Boolean =
        tag != null && (tag.startsWith(TAG_PREFIX) || nodes().any { it.tag == tag })

    fun file(): File = File(Application.application.filesDir, FILE_NAME)

    /** Cached node list; parse failures degrade to an empty list (feature simply stays off). */
    fun nodes(): List<SubXhttpNode> {
        cached?.let { return it }
        val parsed = runCatching {
            val file = file()
            if (!file.isFile) emptyList() else parse(file.readText())
        }.getOrDefault(emptyList())
        cached = parsed
        return parsed
    }

    fun node(tag: String): SubXhttpNode? = nodes().firstOrNull { it.tag == tag }

    private fun parse(text: String): List<SubXhttpNode> {
        if (text.length > MAX_BYTES) return emptyList()
        val root = json.parseToJsonElement(text) as? JsonObject ?: return emptyList()
        val array = root["nodes"] as? JsonArray ?: return emptyList()
        return array.mapNotNull { element ->
            val obj = element as? JsonObject ?: return@mapNotNull null
            val tag = (obj["tag"] as? JsonPrimitive)?.content ?: return@mapNotNull null
            val port = (obj["socksPort"] as? JsonPrimitive)?.content?.toIntOrNull() ?: return@mapNotNull null
            if (port !in 1024..65_535) return@mapNotNull null
            val outbound = obj["outbound"] as? JsonObject ?: return@mapNotNull null
            SubXhttpNode(
                tag = tag,
                label = (obj["label"] as? JsonPrimitive)?.content.orEmpty(),
                socksPort = port,
                address = (obj["address"] as? JsonPrimitive)?.content.orEmpty(),
                outbound = outbound,
            )
        }
    }

    /** Subscription URL + ?format=xray on the same trusted origin. */
    fun manifestUrl(subscriptionUrl: String): String? = runCatching {
        val source = java.net.URI(subscriptionUrl)
        if (!source.scheme.equals("https", true)) return null
        java.net.URI(
            "https", null, source.host, source.port, source.path, "format=xray", null,
        ).toURL().toString()
    }.getOrNull()

    /** Re-fetch the manifest; a failure keeps the previous one (never disturbs a live tunnel). */
    suspend fun refresh(subscriptionUrl: String): Boolean {
        if (!UpdateProfileWork.isTrustedSubUrl(subscriptionUrl)) return false
        val url = manifestUrl(subscriptionUrl) ?: return false
        val fetch = httpFetchSubscription(url, 6_000)
        val body = (fetch as? SubscriptionFetch.Ok)?.body ?: return false
        if (body.length > MAX_BYTES) return false
        val parsed = runCatching { parse(body) }.getOrNull() ?: return false
        if (parsed.isEmpty()) return false
        runCatching {
            val file = file()
            val tmp = File(file.parentFile, file.name + ".tmp")
            tmp.writeText(body)
            if (!tmp.renameTo(file)) {
                file.writeText(body)
                tmp.delete()
            }
        }.onFailure { return false }
        cached = parsed
        resolvedAt = 0L
        return true
    }

    private const val FRESHNESS_MS = 6 * 60 * 60 * 1000L

    /**
     * Make sure a manifest is on disk before the engine starts: [patchDirectRules] can only exempt
     * entry IPs it knows, and a node picked with no rule would loop back into our own tun.
     * Failures are silent — the tunnel must start even when the panel is unreachable.
     */
    suspend fun ensureFresh(subscriptionUrl: String?) {
        if (subscriptionUrl == null) return
        val stored = runCatching { file() }.getOrNull()
        val age = stored?.takeIf { it.isFile }?.let { System.currentTimeMillis() - it.lastModified() }
        if (age != null && age in 0 until FRESHNESS_MS && nodes().isNotEmpty()) return
        runCatching { refresh(subscriptionUrl) }
    }

    /** IPv4 of the node entry, cached for a few minutes; null when it cannot be resolved. */
    fun resolve(node: SubXhttpNode): String? {
        val future = runCatching {
            resolver.submit<String?> { InetAddress.getAllByName(node.address).firstOrNull { it is Inet4Address }?.hostAddress }
        }.getOrNull() ?: return null
        return runCatching { future.get(RESOLVE_TIMEOUT_MS, TimeUnit.MILLISECONDS) }.getOrNull()
    }

    /**
     * IPv4 of the given nodes, resolved in parallel inside one shared budget: the config is built
     * on the service start path, a sequential resolve of ten nodes by 1.5 s would hold it up.
     */
    fun resolveAll(subset: List<SubXhttpNode>): List<String> {
        if (subset.isEmpty()) return emptyList()
        val deadline = SystemClock.elapsedRealtime() + RESOLVE_BUDGET_MS
        val futures = subset.mapNotNull { node ->
            runCatching {
                resolver.submit<String?> { InetAddress.getAllByName(node.address).firstOrNull { it is Inet4Address }?.hostAddress }
            }.getOrNull()
        }
        val ips = futures.mapNotNull { future ->
            val left = (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(1L)
            runCatching { future.get(left, TimeUnit.MILLISECONDS) }.getOrNull()
        }.distinct()
        futures.forEach { it.cancel(true) }
        return ips
    }

    /**
     * IPv4 of every node in [subset], keyed by tag. Used by the all-node runner: one Xray config
     * carries every node, so every outbound needs its own resolved address in one budget.
     */
    fun resolveAddresses(subset: List<SubXhttpNode>): Map<String, String> {
        if (subset.isEmpty()) return emptyMap()
        val deadline = SystemClock.elapsedRealtime() + RESOLVE_BUDGET_MS
        val futures = subset.mapNotNull { node ->
            runCatching {
                node.tag to resolver.submit<String?> {
                    InetAddress.getAllByName(node.address).firstOrNull { it is Inet4Address }?.hostAddress
                }
            }.getOrNull()
        }
        val out = LinkedHashMap<String, String>()
        for ((tag, future) in futures) {
            val left = (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(1L)
            val ip = runCatching { future.get(left, TimeUnit.MILLISECONDS) }.getOrNull()
            if (!ip.isNullOrEmpty()) out[tag] = ip
        }
        futures.forEach { it.second.cancel(true) }
        return out
    }

    /** All node IPs, refreshed lazily (used by the DIRECT rules that keep the child out of the tun). */
    fun nodeIps(): List<String> {
        val all = nodes()
        if (all.isEmpty()) return emptyList()
        val now = SystemClock.elapsedRealtime()
        if (now - resolvedAt < IP_TTL_MS && resolvedIps.isNotEmpty()) return resolvedIps
        val ips = resolveAll(all)
        if (ips.isNotEmpty()) {
            resolvedIps = ips
            resolvedAt = now
        }
        return resolvedIps
    }

    @Volatile private var delays: Map<String, Long> = emptyMap()

    @Volatile private var delaysAt: Map<String, Long> = emptyMap()

    /** Last measured round trip through this node, if any (milliseconds). */
    fun cachedDelay(tag: String?): Long? = if (tag == null) null else delays[tag]

    /** True when the number was probed recently enough to be trusted without a new attempt. */
    fun delayFresh(tag: String?): Boolean {
        val value = tag ?: return false
        val at = delaysAt[value] ?: return false
        // A failed probe is retried much sooner than a successful one: the node may simply have
        // been unreachable for a second, and "недоступен" must not stick for five minutes.
        val ttl = if (delays[value] != null) DELAY_TTL_MS else RETRY_TTL_MS
        return SystemClock.elapsedRealtime() - at < ttl
    }

    /**
     * Fill the latency column of the server list. Only the active node keeps a live SOCKS port, so
     * the remaining ones are probed with a TLS handshake to their entry address; that is the number
     * the owner asked to see next to every server (14.11.2026). A failed probe stays unknown and
     * the row prints "недоступен".
     */
    fun refreshDelays(tags: List<String>) {
        if (tags.isEmpty()) return
        val known = nodes()
        val todo = tags.mapNotNull { tag -> known.firstOrNull { it.tag == tag } }
            .filter { node -> !delayFresh(node.tag) }
            .distinctBy { it.tag }
        if (todo.isEmpty()) return
        // Every node must get a number: the first batch is not the whole list (4 nodes), so the
        // worker walks all missing nodes in batches instead of dropping the rest of the list.
        Thread {
            todo.chunked(PROBE_BATCH).forEach { batch ->
                batch.map { node ->
                    node to probePool.submit<Long?> { delayFor(node) }
                }.forEach { (node, job) ->
                    // Must outlast the probe itself: connect budget + handshake budget.
                    val ms = runCatching { job.get(9, TimeUnit.SECONDS) }.getOrNull()
                    publishDelay(node.tag, ms)
                    SubXhttpProcess.fileLog("delay " + node.tag + " = " + ms)
                }
            }
        }.apply { isDaemon = true; name = "subxhttp-delay-all" }.start()
    }

    /**
     * Real latency of the node: the time until its entry completes a TLS handshake on the phone's
     * own network path. The engine's url-test cannot see these nodes (their port is only open while
     * that node is the active pick), so the list would otherwise show nothing at all.
     */
    fun refreshDelay(node: SubXhttpNode) {
        Thread {
            val ms = delayFor(node)
            publishDelay(node.tag, ms)
            SubXhttpProcess.fileLog("delay " + node.tag + " = " + ms)
        }.apply { isDaemon = true; name = "subxhttp-delay" }.start()
    }

    private fun publishDelay(tag: String, ms: Long?) {
        delaysAt = delaysAt + (tag to SystemClock.elapsedRealtime())
        delays = if (ms != null) delays + (tag to ms) else delays - tag
    }

    /**
     * Reachability of the node's entry, measured on the real path (outside our tun). The session
     * uses it as a gate: a node whose entry does not even complete a TLS handshake must never be
     * selected, because libbox routes the whole phone into its (empty) local SOCKS port the moment
     * the pick lands — that is exactly how the Turkish node killed the owner's internet on
     * 14.11.2026.
     *
     * A bare TCP handshake is NOT enough: the German, Estonian and Polish Akonit entries accepted
     * it in 64-73 ms while answering no data at all (owner's report, 14.11.2026), so the list
     * showed a green ping and the pick then failed — the 1.0.200 TCP gate let the phone go dark.
     */
    fun probeLatency(node: SubXhttpNode): Long? {
        val cached = cachedDelay(node.tag)
        if (cached != null && delayFresh(node.tag)) return cached
        return delayFor(node)
    }

    /**
     * Latency through the node itself when its local runner is alive, otherwise a TLS handshake to
     * its entry. The all-node runner keeps every profile port open, so the list can show a real
     * number for every node — including Barev, whose entry TLS probe is a false negative on the
     * phone path while the tunnel through the local Xray works.
     */
    private fun delayFor(node: SubXhttpNode): Long? {
        val port = liveProfilePorts[node.tag]
        if (port != null && SubXhttpProcess.runningTag() != null) {
            measure(port)?.let { return it }
        }
        return tlsDelay(node)
    }

    /** True once a probe has answered for this tag (success or failure) — the row may say "недоступен". */
    fun probeAttempted(tag: String?): Boolean = tag != null && delaysAt.containsKey(tag)

    /**
     * Time until the entry answers a TLS handshake, in milliseconds; null when it does not.
     *
     * The SNI is the node's own (realitySettings.serverName): a Reality entry without a valid
     * client session — and a probe is exactly that — forwards the handshake to its fallback host,
     * which answers with a real certificate. So success here means «this network path carries
     * data to this entry», and a timeout means the path is dead even though the TCP SYN passes.
     * Measured on 14.11.2026: six working entries answered TLSv1.3 in 339-1802 ms, the three dead
     * ones plus Turkey timed out, while the very same probes from S1 answered all ten in
     * 140-397 ms — the block is on the phone's path, not on the node side.
     */
    private fun tlsDelay(node: SubXhttpNode): Long? {
        val address = resolve(node) ?: node.address
        if (address.isEmpty()) return null
        return try {
            val ip = InetAddress.getByName(address)
            val sni = sniOf(node)
            SubXhttpProcess.fileLog("probe " + node.tag + " -> " + ip.hostAddress + " sni=" + (sni ?: "-"))
            val started = SystemClock.elapsedRealtime()
            openSocket().use { raw ->
                raw.connect(InetSocketAddress(ip, DELAY_TARGET_PORT), IDLE_PROBE_TIMEOUT_MS)
                if (sni == null) {
                    // No TLS name to ask for: fall back to the handshake alone, tagged in the log so
                    // a "green but dead" report can be traced back to a well-formed manifest gap.
                    SubXhttpProcess.fileLog("probe " + node.tag + " has no serverName, tcp-only")
                } else {
                    raw.soTimeout = PROBE_HANDSHAKE_TIMEOUT_MS
                    val tls = probeTls.socketFactory.createSocket(raw, sni, DELAY_TARGET_PORT, true)
                    try {
                        (tls as SSLSocket).startHandshake()
                    } finally {
                        runCatching { tls.close() }
                    }
                }
            }
            SystemClock.elapsedRealtime() - started
        } catch (e: Exception) {
            SubXhttpProcess.fileLog("probe " + node.tag + " tls failed: " + e.javaClass.simpleName + ": " + e.message)
            null
        }
    }

    /** TLS name of the node entry: Reality first, then a plain TLS outbound; null when neither. */
    private fun sniOf(node: SubXhttpNode): String? {
        val stream = node.outbound["streamSettings"] as? JsonObject ?: return null
        val reality = (stream["realitySettings"] as? JsonObject)?.get("serverName")
        val tls = (stream["tlsSettings"] as? JsonObject)?.get("serverName")
        return ((reality ?: tls) as? JsonPrimitive)?.content?.trim()?.takeIf { it.isNotEmpty() }
    }

    /**
     * A probe socket that bypasses our own tun. Without VpnService.protect() the handshake is
     * answered by the engine on the phone itself and every node looks like "1 мс" (the owner saw
     * exactly that on 14.11.2026). When the tunnel is down there is nothing to exclude and the
     * plain socket is already the real path.
     */
    private fun openSocket(): Socket {
        val socket = Socket()
        runCatching { com.maestrovpn.tv.bg.VPNService.current?.protect(socket) }
        return socket
    }

    private fun measure(port: Int): Long? = runCatching {
        val started = SystemClock.elapsedRealtime()
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", port), 2_000)
            socket.soTimeout = 5_000
            val out = socket.getOutputStream()
            val input = socket.getInputStream()
            out.write(byteArrayOf(5, 1, 0))
            out.flush()
            val greeting = ByteArray(2)
            readFully(input, greeting)
            if (greeting[0].toInt() != 5 || greeting[1].toInt() != 0) return null
            val host = DELAY_TARGET_HOST.toByteArray()
            val request = byteArrayOf(5, 1, 0, 3, host.size.toByte()) + host +
                byteArrayOf(0, 1, (DELAY_TARGET_PORT shr 8).toByte(), (DELAY_TARGET_PORT and 0xff).toByte())
            out.write(request)
            out.flush()
            val reply = ByteArray(4)
            readFully(input, reply)
            if (reply[1].toInt() != 0) return null
            SystemClock.elapsedRealtime() - started
        }
    }.getOrNull()

    private fun readFully(input: InputStream, buffer: ByteArray) {
        var offset = 0
        while (offset < buffer.size) {
            val read = input.read(buffer, offset, buffer.size - offset)
            if (read <= 0) throw IllegalStateException("socks: eof")
            offset += read
        }
    }

    /**
     * Xray configuration for one node: local SOCKS in, the subscription outbound (with its address
     * replaced by the resolved IP — the child must not need DNS, a DNS query would go into the tun).
     */
    fun xrayConfig(node: SubXhttpNode, address: String, port: Int = node.socksPort): String {
        val outbound = replaceAddress(node.outbound, address)
        val config = buildJsonObject {
            // Access log off: with it on, every tunnelled connection wrote an "accepted tcp:..."
            // line and the 64 KB diagnostic file rotated away within seconds, which is exactly how
            // the 14.11.2026 failure report lost its own evidence.
            put("log", buildJsonObject { put("loglevel", "warning"); put("access", "none") })
            put("inbounds", buildJsonArray {
                add(buildJsonObject {
                    put("listen", "127.0.0.1")
                    put("port", port)
                    put("protocol", "socks")
                    put("settings", buildJsonObject { put("udp", false) })
                })
            })
            put("outbounds", buildJsonArray { add(outbound) })
        }
        return config.toString()
    }

    /**
     * One Xray configuration for every xhttp node at once: N SOCKS inbounds on the profile ports
     * and N outbounds. The child is a single process, so the engine's urltest can measure all local
     * ports — that is what makes "Авто" cover xhttp/Barev instead of only the ordinary nodes.
     */
    fun xrayConfigAll(
        entries: List<Pair<SubXhttpNode, Int>>,
        addresses: Map<String, String>,
    ): String {
        require(entries.isNotEmpty()) { "xhttp entries required" }
        val config = buildJsonObject {
            put("log", buildJsonObject { put("loglevel", "warning"); put("access", "none") })
            put("inbounds", buildJsonArray {
                entries.forEach { (_, port) ->
                    add(buildJsonObject {
                        put("tag", allInboundTag(port))
                        put("listen", "127.0.0.1")
                        put("port", port)
                        put("protocol", "socks")
                        put("settings", buildJsonObject { put("udp", false) })
                    })
                }
            })
            put("outbounds", buildJsonArray {
                entries.forEach { (node, port) ->
                    add(
                        JsonObject(
                            replaceAddress(node.outbound, addresses[node.tag] ?: node.address) +
                                ("tag" to JsonPrimitive(allOutboundTag(port))),
                        ),
                    )
                }
            })
            // Without explicit routing every SOCKS inbound would use the FIRST outbound: all
            // ports would look alive but carry the wrong node's traffic. Bind each inbound to its
            // own outbound, one rule per node.
            put("routing", buildJsonObject {
                put("rules", buildJsonArray {
                    entries.forEach { (_, port) ->
                        add(buildJsonObject {
                            put("type", "field")
                            put("inboundTag", JsonArray(listOf(JsonPrimitive(allInboundTag(port)))))
                            put("outboundTag", JsonPrimitive(allOutboundTag(port)))
                        })
                    }
                })
            })
        }
        return config.toString()
    }

    private fun allInboundTag(port: Int): String = "socks-" + port

    private fun allOutboundTag(port: Int): String = "node-" + port

    private fun replaceAddress(outbound: JsonObject, address: String): JsonObject {
        val settings = outbound["settings"] as? JsonObject ?: return outbound
        val vnext = settings["vnext"] as? JsonArray ?: return outbound
        val first = vnext.firstOrNull() as? JsonObject ?: return outbound
        val patched = JsonObject(first + ("address" to JsonPrimitive(address)))
        val patchedVnext = JsonArray(listOf(patched) + vnext.drop(1))
        val patchedSettings = JsonObject(settings + ("vnext" to patchedVnext))
        return JsonObject(outbound + ("settings" to patchedSettings))
    }

    /**
     * A child process cannot call VpnService.protect(), so its own TCP to the entry node would be
     * captured by our tun and loop back into itself. Same fix the CDN path uses: an explicit
     * DIRECT rule for the entry IPs, placed before anything that could claim them.
     */
    fun patchDirectRules(config: String): String {
        val root = runCatching { json.parseToJsonElement(config) as? JsonObject }.getOrNull() ?: return config
        val outbounds = root["outbounds"] as? JsonArray ?: return config
        // Which of our nodes are actually in this engine config — matched by TAG, not by the port
        // remembered in the manifest: after a subscription reload the profile renumbers the nodes,
        // and a byte-equal node must still get its DIRECT rule (and its live runner port).
        val ports = SubXhttpPlan.profileXhttpPorts(config)
        liveProfilePorts = ports
        val known = nodes()
        if (known.isEmpty()) {
            SubXhttpProcess.fileLog("direct skipped: no nodes")
            return config
        }
        val active = known.filter { ports.containsKey(it.tag) }
        if (active.isEmpty()) {
            SubXhttpProcess.fileLog("direct skipped: nodes=" + known.size + " none in config")
            return config
        }
        val ips = resolveAll(active)
        if (ips.isEmpty()) {
            SubXhttpProcess.fileLog("direct skipped: resolve returned nothing for " + active.size + " nodes")
            return config
        }
        val withAuto = addXhttpToAuto(root, outbounds, ports)
        val route = withAuto["route"] as? JsonObject ?: return config
        val rules = route["rules"] as? JsonArray ?: return config
        val directTag = outbounds.firstNotNullOfOrNull { element ->
            val obj = element as? JsonObject ?: return@firstNotNullOfOrNull null
            if ((obj["type"] as? JsonPrimitive)?.content == "direct") (obj["tag"] as? JsonPrimitive)?.content else null
        } ?: "direct"
        val rule = buildJsonObject {
            put("action", "route")
            put("ip_cidr", JsonArray(ips.map { JsonPrimitive(it + "/32") }))
            put("outbound", directTag)
        }
        val list = rules.toMutableList()
        var insertAt = 0
        while (insertAt < list.size) {
            val action = ((list[insertAt] as? JsonObject)?.get("action") as? JsonPrimitive)?.content
            if (action == "sniff" || action == "hijack-dns") insertAt++ else break
        }
        list.add(insertAt, rule)
        appliedIps = ips.toSet()
        // The DIRECT set used to be invisible, so "the node is dead" could not be told apart from
        // "the rule never covered its IP". Logged on every config build on purpose (14.11.2026).
        SubXhttpProcess.fileLog(
            "direct nodes=" + known.size + " present=" + active.size + " ips=" + ips.size + " " + ips,
        )
        val patchedRoute = JsonObject(route + ("rules" to JsonArray(list)))
        return JsonObject(withAuto + ("route" to patchedRoute)).toString()
    }

    /**
     * Add the local xhttp socks stubs to the urltest "auto" group of OUR copy of the engine config.
     * The shim deliberately keeps them out of auto (old clients have no all-node runner); this app
     * starts every runner port, so auto can and must test them too. Done locally, so the server
     * response and every older client stay byte-identical.
     */
    private fun addXhttpToAuto(root: JsonObject, outbounds: JsonArray, ports: Map<String, Int>): JsonObject {
        val index = outbounds.indexOfFirst { element ->
            val obj = element as? JsonObject ?: return@indexOfFirst false
            (obj["type"] as? JsonPrimitive)?.content == "urltest" &&
                (obj["tag"] as? JsonPrimitive)?.content == "auto"
        }
        if (index < 0) return root
        val auto = outbounds[index] as? JsonObject ?: return root
        val rawMembers = auto["outbounds"] as? JsonArray ?: return root
        // AutoSelectPool already removes our own servers from a mixed group; a group that held
        // nothing but our servers was left untouched (an empty urltest is rejected by the engine).
        // Adding xhttp stubs makes such a group non-empty, so drop the own tags here as well.
        val members = rawMembers.filterNot { element ->
            val tag = (element as? JsonPrimitive)?.content
            tag != null && OwnServers.isOwnTag(tag)
        }
        val existing = members.mapNotNull { (it as? JsonPrimitive)?.content }.toSet()
        val missing = ports.keys.filter { it !in existing }
        if (missing.isEmpty() && members.size == rawMembers.size) return root
        val merged = JsonArray(members.toList() + missing.map { JsonPrimitive(it) })
        val patchedAuto = JsonObject(auto + ("outbounds" to merged))
        val patched = outbounds.toMutableList().also { it[index] = patchedAuto }
        SubXhttpProcess.fileLog(
            "auto +" + missing.size + " xhttp outbounds, own removed=" + (rawMembers.size - members.size),
        )
        return JsonObject(root + ("outbounds" to JsonArray(patched)))
    }

    /** Test/DI hook: drop the in-memory copy after the manifest file was replaced. */
    fun invalidate() {
        cached = null
        resolvedAt = 0L
        resolvedIps = emptyList()
    }
}

package com.maestrovpn.tv.bg

import android.content.Context
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/**
 * Brings up a local SOCKS5 proxy for mieru, because sing-box has no native mieru
 * outbound. sing-box dials `127.0.0.1:<socksPort>` (the subgen `mieru` socks
 * outbound); mieru authenticates to the mita server with the customer's creds,
 * fetched from `GET <subUrl>/helpers`.
 *
 * PRIMARY path: the mieru client is bound IN-PROCESS into libbox.aar (class
 * `io.nekohasekai.mierubridge.Mierubridge`, one shared Go runtime). It is started
 * via reflection so the app still builds/runs even against a libbox without it.
 * This is immune to the Android 12+ phantom-process killer and works on every ABI.
 *
 * FALLBACK path: if that class is absent (older libbox) or refuses to start, we
 * exec the bundled `libmieru.so` binary as a child process — the original
 * mechanism — and supervise it with backoff while [running].
 *
 * Everything — creds fetch, config build, start — runs on a daemon thread, so the
 * VPN start path is never blocked.
 */
class MieruHelper(private val context: Context) {
    @Volatile private var process: Process? = null
    @Volatile private var running = false
    @Volatile private var inProcess = false
    // The local SOCKS5 port, published once creds are fetched (0 = backend not up yet).
    // awaitReady() polls it so a caller can gate the sing-box (re)load on it.
    @Volatile private var socksPort = 0
    private var thread: Thread? = null

    private val binary: File
        get() = File(context.applicationInfo.nativeLibraryDir, "libmieru.so")

    // The in-process mieru client, bound into libbox.aar. Resolved by reflection so
    // a build whose libbox lacks it simply falls back to the exec'd binary.
    private val bridgeClass: Class<*>? by lazy {
        runCatching { Class.forName("io.nekohasekai.mierubridge.Mierubridge") }.getOrNull()
    }

    /** True if mieru can run here — in-process (any ABI, primary) or via the exec binary. */
    fun isAvailable(): Boolean = bridgeClass != null || (binary.exists() && binary.canExecute())

    /**
     * Start the helper for a Remote profile's subscription [subUrl]. No-op if the
     * binary is absent, the URL is blank, or the customer has no mieru protocol.
     */
    @Synchronized
    fun start(subUrl: String) {
        if (running || subUrl.isBlank() || !isAvailable()) return
        running = true
        thread = Thread { run(subUrl.trimEnd('/')) }.apply { isDaemon = true; start() }
    }

    @Synchronized
    fun stop() {
        running = false
        socksPort = 0
        // Tear the in-process bridge down UNCONDITIONALLY (Mierubridge.stop is mutex-
        // guarded + idempotent — a no-op when nothing is running). Gating on `inProcess`
        // had a TOCTOU hole: a stop landing between Mierubridge.start() returning (socks5
        // already listening) and `inProcess=true` would skip teardown → a leaked socks5
        // server that nothing reaps, and mieru un-restartable (bridge no-ops on server!=nil).
        if (bridgeClass != null) {
            runCatching { bridgeClass?.getMethod("stop")?.invoke(null) }
        }
        inProcess = false
        process?.destroy()
        process = null
        thread?.interrupt()
        thread = null
    }

    /**
     * Blocks up to [timeoutMs] until the local SOCKS5 backend is accepting connections, so a
     * caller can (re)load the sing-box config — whose `mieru` outbound dials 127.0.0.1:<socks> —
     * only AFTER the backend is actually listening. Without this, a config reload races the
     * async [start]: sing-box probes a dead port and the mieru outbound (and, on reload, the
     * whole tunnel) breaks until a full app restart. Returns true if it came up; false on
     * timeout (the caller then proceeds — urltest simply marks mieru unhealthy until it appears).
     */
    fun awaitReady(timeoutMs: Long): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            val port = socksPort
            if (port > 0 && runCatching {
                    java.net.Socket().use {
                        it.connect(java.net.InetSocketAddress("127.0.0.1", port), 500); true
                    }
                }.getOrDefault(false)
            ) {
                return true
            }
            try {
                Thread.sleep(150)
            } catch (_: InterruptedException) {
                return false
            }
        }
        return false
    }

    private fun run(subUrlRaw: String) {
        // The stored profile URL may carry a ?device=<id> query (the per-account device
        // cap param, used only on the config poll). /helpers and /mierulog are path-based,
        // and "$subUrl/helpers" on a query-bearing URL mangles the path into
        // ".../sub/<tok>?device=<id>/helpers" → the panel serves the config, not the creds,
        // and Mieru can't start. Strip the query here.
        val subUrl = subUrlRaw.substringBefore("?")
        // Diagnostics → panel /mierulog/<token> (read server-side via journalctl), so
        // the REAL failure (exec error, bad config, mita auth/conn error) is visible
        // without device access.
        val logUrl = subUrl.replace("/sub/", "/mierulog/")
        fun report(msg: String) {
            runCatching { postLog(logUrl, msg) }
        }
        report("start isAvailable=${isAvailable()} bin=${binary.absolutePath} exists=${binary.exists()} exec=${binary.canExecute()}")

        val creds = runCatching { fetchCreds("$subUrl/helpers") }.getOrNull()
        if (creds == null) {
            Log.i(TAG, "no mieru creds for this subscription — helper idle")
            report("no mieru creds (customer has no mieru / fetch failed)")
            running = false
            return
        }
        socksPort = creds.socksPort // publish for awaitReady() before the backend is brought up
        val dir = File(context.filesDir, "mieru").apply { mkdirs() }
        val cfg = buildConfig(creds)
        val configJson = File(dir, "client.json").apply { writeText(cfg) }
        report("config $cfg")

        // Prefer in-process mieru (bound into libbox.aar): one Go runtime, immune to
        // the Android 12+ phantom-process killer, no forked binary. Fall back to the
        // exec'd libmieru.so only if the library is absent or refuses to start.
        if (bridgeClass != null && startInProcess(cfg) { report(it) }) {
            inProcess = true
            report("in-process mieru started (libbox-embedded)")
            verifySocks(creds.socksPort) { report(it) }
            return // the in-process socks5 server runs until stop(); nothing to supervise
        }
        if (!binary.exists() || !binary.canExecute()) {
            report("in-process failed and no exec fallback on this ABI — mieru idle")
            running = false
            return
        }
        report("in-process unavailable — falling back to exec'd libmieru.so")

        // `mieru run` loads its config DIRECTLY from MIERU_CONFIG_JSON_FILE and serves
        // SOCKS5 in the foreground (documented path; no `apply config` step).
        var backoff = 1000L
        while (running) {
            try {
                val p = exec(configJson, "run")
                process = p
                // After a moment, report whether mieru's local SOCKS5 actually came up.
                Thread {
                    runCatching {
                        Thread.sleep(3500)
                        val up = runCatching {
                            java.net.Socket().use {
                                it.connect(java.net.InetSocketAddress("127.0.0.1", creds.socksPort), 2000); true
                            }
                        }.getOrDefault(false)
                        report("socks5 127.0.0.1:${creds.socksPort} up=$up")
                    }
                }.apply { isDaemon = true }.start()
                // Stream mieru's own output and report the first lines (its startup
                // banner / connection errors) so the actual runtime state is visible.
                var reported = 0
                p.inputStream.bufferedReader().forEachLine {
                    Log.i(TAG, "mieru: $it")
                    if (reported < 25) {
                        report("out $it")
                        reported++
                    }
                }
                val code = p.waitFor()
                Log.w(TAG, "mieru run exited ($code)")
                report("run exited code=$code")
            } catch (_: InterruptedException) {
                break
            } catch (e: Exception) {
                Log.e(TAG, "mieru run error", e)
                report("run EXEC FAILED ${e.javaClass.simpleName}: ${e.message}")
            }
            if (!running) break
            try {
                Thread.sleep(backoff)
            } catch (_: InterruptedException) {
                break
            }
            backoff = (backoff * 2).coerceAtMost(15_000L)
        }
    }

    /** Start the in-process mieru via reflection into libbox's embedded bridge. */
    private fun startInProcess(cfg: String, report: (String) -> Unit): Boolean = runCatching {
        bridgeClass!!.getMethod("start", String::class.java).invoke(null, cfg)
        true
    }.getOrElse { e ->
        val cause = (e as? java.lang.reflect.InvocationTargetException)?.targetException ?: e
        Log.e(TAG, "in-process mieru start failed", cause)
        report("in-process start FAILED ${cause.javaClass.simpleName}: ${cause.message}")
        false
    }

    /** Off-thread reachability check of the local socks5 port, for server-side diagnostics. */
    private fun verifySocks(socksPort: Int, report: (String) -> Unit) {
        Thread {
            runCatching {
                Thread.sleep(800)
                val up = runCatching {
                    java.net.Socket().use {
                        it.connect(java.net.InetSocketAddress("127.0.0.1", socksPort), 2000); true
                    }
                }.getOrDefault(false)
                report("in-process socks5 127.0.0.1:$socksPort up=$up")
            }
        }.apply { isDaemon = true }.start()
    }

    private fun postLog(url: String, msg: String) {
        val c = (URL(url).openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            doOutput = true
            connectTimeout = 10_000
            readTimeout = 10_000
        }
        try {
            c.outputStream.use { it.write(msg.toByteArray()) }
            c.responseCode
        } finally {
            c.disconnect()
        }
    }

    private fun exec(configJson: File, vararg args: String): Process =
        ProcessBuilder(listOf(binary.absolutePath) + args)
            .redirectErrorStream(true)
            .also { it.environment()["MIERU_CONFIG_JSON_FILE"] = configJson.absolutePath }
            .start()

    private fun fetchCreds(url: String): Creds? {
        val c = (URL(url).openConnection() as HttpURLConnection).apply {
            connectTimeout = 15_000
            readTimeout = 15_000
        }
        try {
            if (c.responseCode != 200) return null
            val o = JSONObject(c.inputStream.bufferedReader().use { it.readText() })
            val m = o.optJSONObject("mieru") ?: return null
            return Creds(
                server = m.getString("server"),
                port = m.getInt("port"),
                username = m.getString("username"),
                password = m.getString("password"),
                transport = m.optString("transport", "TCP"),
                socksPort = m.getInt("socks"),
            )
        } finally {
            c.disconnect()
        }
    }

    private fun buildConfig(c: Creds): String {
        val binding = JSONObject()
            .put("port", c.port)
            .put("protocol", c.transport.ifBlank { "TCP" })
        val server = JSONObject()
            .put("ipAddress", c.server)
            .put("domainName", "")
            .put("portBindings", JSONArray().put(binding))
        val profile = JSONObject()
            .put("profileName", PROFILE)
            .put("user", JSONObject().put("name", c.username).put("password", c.password))
            .put("servers", JSONArray().put(server))
            // Lowered 1400→1280: mieru pads EVERY datagram up to its mtu (anti-fingerprinting),
            // so mtu=1400 emits ~1428-byte UDP datagrams that get blackholed on the constrained RU
            // uplink → upload stalled to ~0 while download (server-sized) was fine. 1280 (IPv6 min)
            // → ~1308-byte datagrams that fit the uplink. This is the CLIENT/upload side; the mita
            // server stays 1400 for its download egress — a client mtu below the server's is safe
            // (verified: a 1280 client passes traffic against the live 1400 server).
            .put("mtu", 1280)
        // No rpcPort → it stays 0 = RPC disabled, which `mieru run` correctly skips
        // (it only needs the foreground SOCKS5 server). socks5Port is the only port.
        return JSONObject()
            .put("profiles", JSONArray().put(profile))
            .put("activeProfile", PROFILE)
            .put("socks5Port", c.socksPort)
            .put("socks5ListenLAN", false)
            .put("loggingLevel", "INFO")
            .toString()
    }

    private data class Creds(
        val server: String,
        val port: Int,
        val username: String,
        val password: String,
        val transport: String,
        val socksPort: Int,
    )

    companion object {
        private const val TAG = "MieruHelper"
        private const val PROFILE = "maestro"
    }
}

package com.maestrovpn.tv.bg

import android.content.Context
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/**
 * Runs the bundled Mieru client (`libmieru.so`) as a local SOCKS5 helper, because
 * sing-box has no native mieru outbound. sing-box dials `127.0.0.1:<socksPort>`
 * (the subgen `mieru` socks outbound); this process authenticates to the mita
 * server with the customer's creds, fetched from `GET <subUrl>/helpers`.
 *
 * The `mieru run` subcommand serves SOCKS5 in the foreground (no daemon/RPC), so
 * we launch it, stream its log, and restart it with backoff while [running].
 * Everything — the creds fetch, config write, and run loop — happens on a daemon
 * thread, so the VPN start path is never blocked. Bundled arm64-only: [isAvailable]
 * is false on other ABIs, where mieru is simply unavailable and the selector falls
 * back to the other protocols.
 */
class MieruHelper(private val context: Context) {
    @Volatile private var process: Process? = null
    @Volatile private var running = false
    private var thread: Thread? = null

    private val binary: File
        get() = File(context.applicationInfo.nativeLibraryDir, "libmieru.so")

    /** True only on a device that ships the arm64 helper binary. */
    fun isAvailable(): Boolean = binary.exists() && binary.canExecute()

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
        process?.destroy()
        process = null
        thread?.interrupt()
        thread = null
    }

    private fun run(subUrl: String) {
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
        val dir = File(context.filesDir, "mieru").apply { mkdirs() }
        val cfg = buildConfig(creds)
        val configJson = File(dir, "client.json").apply { writeText(cfg) }
        report("config $cfg")

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
            .put("mtu", 1400) // MUST match the mita server's mtu (1400)
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

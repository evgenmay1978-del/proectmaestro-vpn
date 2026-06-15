package com.maestrovpn.tv.bg

import android.content.Context
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * Runs the bundled Mieru client (`libmieru.so`) as a local SOCKS5 helper, because
 * sing-box has no native mieru outbound. sing-box dials `127.0.0.1:<socksPort>`
 * (the [subgen] `mieru` socks outbound); this process authenticates to the mita
 * server with the customer's creds (fetched from `GET /sub/<tok>/helpers`).
 *
 * The `mieru run` subcommand serves SOCKS5 in the foreground (no daemon/RPC), so
 * we just launch it, stream its log, and restart it with backoff while [running].
 * Bundled arm64-only — [isAvailable] is false on other ABIs, where mieru is simply
 * unavailable and the selector falls back to the other protocols.
 *
 * Lifecycle: [start] when the VPN comes up and mieru is provisioned, [stop] on
 * teardown.
 */
class MieruHelper(private val context: Context) {
    data class Creds(
        val server: String,
        val port: Int,
        val username: String,
        val password: String,
        val transport: String,
        val socksPort: Int,
    )

    @Volatile private var process: Process? = null
    @Volatile private var running = false
    private var thread: Thread? = null

    private val binary: File
        get() = File(context.applicationInfo.nativeLibraryDir, "libmieru.so")

    /** True only on a device that ships the arm64 helper binary. */
    fun isAvailable(): Boolean = binary.exists() && binary.canExecute()

    @Synchronized
    fun start(c: Creds) {
        if (running) return
        if (!isAvailable()) {
            Log.w(TAG, "mieru helper binary missing (non-arm64 device?) — skipping")
            return
        }
        val dir = File(context.filesDir, "mieru").apply { mkdirs() }
        val configJson = File(dir, "client.json").apply { writeText(buildConfig(c)) }
        val configPb = File(dir, "client.conf.pb")
        running = true
        thread = Thread { supervise(configJson, configPb) }.apply { isDaemon = true; start() }
    }

    @Synchronized
    fun stop() {
        running = false
        process?.destroy()
        process = null
        thread?.interrupt()
        thread = null
    }

    private fun exec(vararg args: String, configPb: File): Process =
        ProcessBuilder(listOf(binary.absolutePath) + args)
            .redirectErrorStream(true)
            .also { it.environment()["MIERU_CONFIG_FILE"] = configPb.absolutePath }
            .start()

    private fun supervise(configJson: File, configPb: File) {
        // Apply the config once — writes the protobuf the `run` command reads.
        runCatching { exec("apply", "config", configJson.absolutePath, configPb = configPb).waitFor() }
            .onFailure { Log.e(TAG, "mieru apply config failed", it) }

        var backoff = 1000L
        while (running) {
            try {
                val p = exec("run", configPb = configPb)
                process = p
                p.inputStream.bufferedReader().forEachLine { Log.i(TAG, "mieru: $it") }
                Log.w(TAG, "mieru run exited (${p.waitFor()})")
            } catch (_: InterruptedException) {
                break
            } catch (e: Exception) {
                Log.e(TAG, "mieru run error", e)
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

    /** Build the mieru client config JSON from the customer's mita creds. */
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
        return JSONObject()
            .put("profiles", JSONArray().put(profile))
            .put("activeProfile", PROFILE)
            .put("socks5Port", c.socksPort)
            .put("socks5ListenLAN", false)
            .toString()
    }

    companion object {
        private const val TAG = "MieruHelper"
        private const val PROFILE = "maestro"
    }
}

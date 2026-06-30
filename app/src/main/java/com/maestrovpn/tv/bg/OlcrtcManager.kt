package com.maestrovpn.tv.bg

import android.util.Log
import com.maestrovpn.tv.Application
import java.io.File
import java.net.InetSocketAddress
import java.net.Socket

/**
 * olcRTC — an opt-in WebRTC video-disguise FALLBACK transport (the AmneziaWG slot), wired as a
 * SEPARATE-PROCESS native binary (option-b), NOT a gomobile binding.
 *
 * Why a child process: the old gomobile `olcrtc.aar` collided with libbox's `go.*` JNI runtime
 * ("Duplicate class go.Seq"). Instead we ship olcRTC's standalone `cmd/olcrtc` as
 * `jniLibs/<abi>/libolcrtc.so` (built by `.github/workflows/olcrtc-bin.yml`, downloaded into the
 * APK by `android.yml`). `useLegacyPackaging=true` extracts it to `nativeLibraryDir`, so we can
 * exec it. It JOINs a Yandex Telemost (or Jitsi) video call, disguises the tunnel as that call,
 * and bridges it to a local SOCKS5 at 127.0.0.1:[SOCKS_PORT]. The sing-box config (from /sub,
 * for the owner's login only) has a `socks` outbound named [OUTBOUND_TAG] pointing there, plus a
 * DIRECT route for the carrier hosts so the child's own WebRTC sockets don't loop the tun.
 *
 * Lifecycle: lazy. The child starts ONLY when the user picks the olcRTC item in the protocol
 * selector ([GroupsViewModel.selectGroupItem] calls [ensureStarted] before `selectOutbound`), and
 * is killed when the user switches away or the tunnel stops ([BoxService.stopService] → [stop]).
 * The WebRTC params (provider/room/key/transport) arrive over GET /sub/<tok>/info (gated to the
 * owner) and are pushed in via [setCreds]; without them [ensureStarted] is a no-op → "locked".
 *
 * Single app process (no android:process in the manifest), so this singleton is shared by the UI
 * (selector) and the VPN service. ⛔ Gated server-side to the owner's login — inert for the fleet.
 */
object OlcrtcManager {
    const val OUTBOUND_TAG = "olcrtc"
    const val SOCKS_HOST = "127.0.0.1"
    const val SOCKS_PORT = 8808

    private const val TAG = "OlcrtcManager"
    private const val READY_TIMEOUT_MS = 25_000L
    private const val POLL_INTERVAL_MS = 300L

    private data class Creds(val provider: String, val room: String, val key: String, val transport: String)

    @Volatile private var creds: Creds? = null
    @Volatile private var process: Process? = null
    private val lock = Any()

    /** Push the WebRTC params delivered over /sub/<tok>/info (owner-gated). Empty/blank → cleared. */
    fun setCreds(provider: String?, room: String?, key: String?, transport: String?) {
        creds = if (!provider.isNullOrBlank() && !room.isNullOrBlank() && !key.isNullOrBlank()) {
            Creds(provider.trim(), room.trim(), key.trim(), transport?.trim()?.ifBlank { "vp8channel" } ?: "vp8channel")
        } else {
            null
        }
    }

    /** True when the olcRTC entry should be UNLOCKED (creds delivered + the binary is bundled). */
    fun isUnlocked(): Boolean = creds != null && binaryFile()?.exists() == true

    fun hasCreds(): Boolean = creds != null

    val isRunning: Boolean get() = process?.let { alive(it) } == true

    // minSdk 23 — Process.isAlive()/waitFor(timeout)/destroyForcibly() are API 26+, so probe
    // liveness the portable way (exitValue throws while still running).
    private fun alive(p: Process): Boolean =
        try { p.exitValue(); false } catch (_: IllegalThreadStateException) { true }

    /**
     * Start the olcRTC child (if not already up) and block until its SOCKS5 listener accepts, up
     * to [READY_TIMEOUT_MS]. Returns true when the local SOCKS5 is ready to carry traffic. MUST be
     * called off the main thread (it blocks). Idempotent: a live+listening child returns true fast.
     */
    fun ensureStarted(): Boolean {
        synchronized(lock) {
            val c = creds ?: run { Log.w(TAG, "ensureStarted: no creds (locked)"); return false }
            if (isRunning && portOpen()) return true
            stopLocked() // clear any half-dead child before respawning

            val bin = binaryFile()
            if (bin == null || !bin.exists()) {
                Log.e(TAG, "ensureStarted: libolcrtc.so not bundled in this build — olcRTC unavailable")
                return false
            }
            val dir = File(Application.application.filesDir, "olcrtc").apply { mkdirs() }
            val dataDir = File(dir, "data").apply { mkdirs() }
            val yaml = File(dir, "client.yaml")
            yaml.writeText(buildClientYaml(c, dataDir.absolutePath))

            try {
                val p = ProcessBuilder(bin.absolutePath, yaml.absolutePath)
                    .directory(dir)
                    .redirectErrorStream(true)
                    .start()
                process = p
                drainLogs(p)
                Log.i(TAG, "olcRTC child started (pid via ${bin.name}); waiting for SOCKS5 :$SOCKS_PORT")
            } catch (e: Exception) {
                Log.e(TAG, "ensureStarted: exec failed: ${e.message}", e)
                return false
            }
        }
        // Poll the port OUTSIDE the lock so stop() can still interrupt a stuck start.
        val deadline = System.currentTimeMillis() + READY_TIMEOUT_MS
        while (System.currentTimeMillis() < deadline) {
            val p = process ?: return false
            if (!alive(p)) {
                Log.e(TAG, "olcRTC child exited early (code=${runCatching { p.exitValue() }.getOrNull()}) — see olcRTC logs above")
                return false
            }
            if (portOpen()) {
                Log.i(TAG, "olcRTC SOCKS5 ready")
                return true
            }
            try {
                Thread.sleep(POLL_INTERVAL_MS)
            } catch (_: InterruptedException) {
                return false
            }
        }
        Log.e(TAG, "olcRTC SOCKS5 did not open within ${READY_TIMEOUT_MS}ms — giving up")
        stop()
        return false
    }

    /** Kill the child process (idempotent). Called on switch-away and on tunnel stop. */
    fun stop() {
        synchronized(lock) { stopLocked() }
    }

    private fun stopLocked() {
        val p = process ?: return
        process = null
        runCatching {
            p.destroy() // SIGTERM (== SIGKILL on the runtimes we ship to); no destroyForcibly (API 26+)
            // Best-effort reap so a zombie doesn't linger, without the API-26 waitFor(timeout).
            val until = System.currentTimeMillis() + 2_000
            while (alive(p) && System.currentTimeMillis() < until) Thread.sleep(50)
        }
        Log.i(TAG, "olcRTC child stopped")
    }

    private fun binaryFile(): File? =
        runCatching { File(Application.application.applicationInfo.nativeLibraryDir, "libolcrtc.so") }.getOrNull()

    private fun portOpen(): Boolean = runCatching {
        Socket().use { it.connect(InetSocketAddress(SOCKS_HOST, SOCKS_PORT), 400); true }
    }.getOrDefault(false)

    private fun drainLogs(p: Process) {
        Thread({
            runCatching {
                p.inputStream.bufferedReader().forEachLine { line -> Log.i("olcRTC", line) }
            }
        }, "olcrtc-logs").apply { isDaemon = true; start() }
    }

    /**
     * The cnc-mode client config (mirrors olcrtc's telemost+vp8channel client example). The carrier's
     * own DNS is pinned to a RU resolver (77.88.8.8) so the child's lookups route DIRECT (geoip-ru)
     * and don't loop back through the olcRTC outbound. room/key are quoted (room is a URL).
     */
    private fun buildClientYaml(c: Creds, dataDir: String): String = buildString {
        appendLine("mode: cnc")
        appendLine("auth:")
        appendLine("  provider: ${c.provider}")
        appendLine("room:")
        appendLine("  id: \"${c.room}\"")
        appendLine("crypto:")
        appendLine("  key: \"${c.key}\"")
        appendLine("net:")
        appendLine("  transport: ${c.transport}")
        appendLine("  dns: \"77.88.8.8:53\"")
        appendLine("liveness:")
        appendLine("  interval: 10s")
        appendLine("  timeout: 5s")
        appendLine("  failures: 3")
        appendLine("socks:")
        appendLine("  host: \"$SOCKS_HOST\"")
        appendLine("  port: $SOCKS_PORT")
        appendLine("vp8:")
        appendLine("  fps: 30")
        appendLine("  batch_size: 64")
        appendLine("data: $dataDir")
    }
}

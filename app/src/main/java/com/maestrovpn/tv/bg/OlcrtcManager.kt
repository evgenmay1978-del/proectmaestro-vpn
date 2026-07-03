package com.maestrovpn.tv.bg

import android.content.Context
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
 * ⚠️ A separate process CANNOT call VpnService.protect() (it's an in-process binder bound to the
 * VPNService), so there is NO OS-level socket bypass: the child's traffic IS captured by the tun
 * and loop-avoidance rests ENTIRELY on the backend's route rules (carrier domains + static Yandex
 * CIDRs DIRECT + carrier DNS → local), emitted only for the gated login. Route-log-verify on a
 * real device before relying on it.
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
    private const val CREDS_PREFS = "maestro_olcrtc"

    private data class Creds(val provider: String, val room: String, val key: String, val transport: String)

    @Volatile private var creds: Creds? = null
    @Volatile private var loaded = false
    @Volatile private var process: Process? = null
    private val lock = Any()

    private val tokenRe = Regex("^[a-z0-9]{1,32}$")
    private val keyRe = Regex("^[0-9a-fA-F]{16,128}$")

    /**
     * Push the WebRTC params delivered over /sub/<tok>/info (owner-gated). Absent → cleared
     * (locked). VALIDATED before use: these values are templated into a YAML the child reads,
     * so provider/transport must be a lowercase token, key hex, room an http(s) URL with no
     * whitespace/quote/control char — any failure clears creds rather than writing a malformed
     * or injectable client.yaml. Owner-controlled today, but cheap defense-in-depth.
     */
    fun setCreds(provider: String?, room: String?, key: String?, transport: String?) {
        val p = provider?.trim().orEmpty()
        val r = room?.trim().orEmpty()
        val k = key?.trim().orEmpty()
        val t = transport?.trim().orEmpty().ifBlank { "vp8channel" }
        val valid = p.matches(tokenRe) && k.matches(keyRe) && t.matches(tokenRe) &&
            (r.startsWith("https://") || r.startsWith("http://")) &&
            r.none { it.isWhitespace() || it == '"' || it == '\'' || it.code < 0x20 }
        creds = if (valid) Creds(p, r, k, t) else null
        loaded = true
        // Persist the unlock (or the revoke) so it survives an app restart AND — crucially — a launch
        // where /info can't be reached (behind an ISP whitelist that blocks the panel): the child can
        // still start from cache. Cleared when a SUCCESSFUL /info returns no olcrtc (owner revoked).
        // A FAILED /info never calls setCreds, so the cached unlock is kept.
        runCatching {
            val e = credsPrefs().edit()
            val c = creds
            if (c != null) {
                e.putString("p", c.provider).putString("r", c.room).putString("k", c.key).putString("t", c.transport)
            } else {
                e.clear()
            }
            e.apply()
        }
    }

    private fun credsPrefs() = Application.application.getSharedPreferences(CREDS_PREFS, Context.MODE_PRIVATE)

    // Load the persisted unlock ONCE so hasCreds()/isUnlocked() are correct right at launch — BEFORE
    // any /info round-trip — which is what lets olcRTC work behind a whitelist that blocks the panel.
    private fun ensureLoaded() {
        if (loaded) return
        synchronized(lock) {
            if (loaded) return
            runCatching {
                val sp = credsPrefs()
                val p = sp.getString("p", null)
                val r = sp.getString("r", null)
                val k = sp.getString("k", null)
                val t = sp.getString("t", null)
                if (p != null && r != null && k != null && t != null) creds = Creds(p, r, k, t)
            }
            loaded = true
        }
    }

    /** True when the olcRTC entry should be UNLOCKED (creds delivered + the binary is bundled). */
    fun isUnlocked(): Boolean { ensureLoaded(); return creds != null && binaryFile()?.exists() == true }

    fun hasCreds(): Boolean { ensureLoaded(); return creds != null }

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
        ensureLoaded() // creds may live only in the persisted cache (e.g. this launch never hit /info)
        val started: Process
        synchronized(lock) {
            val c = creds ?: run { Log.w(TAG, "ensureStarted: no creds (locked)"); return false }
            if (isRunning && portOpen()) return true
            stopLocked() // kill our tracked child (if any) before respawning
            // Reap any ORPHAN libolcrtc.so left by a previous app-process instance that was
            // OS-killed (low-mem / swipe-away / START_STICKY restart): a fresh singleton has
            // process=null but the orphan still holds :8808, so a new exec would bind-fail.
            reapStaleChildren()

            val bin = binaryFile()
            if (bin == null || !bin.exists()) {
                Log.e(TAG, "ensureStarted: libolcrtc.so not bundled in this build — olcRTC unavailable")
                return false
            }
            val dir = File(Application.application.filesDir, "olcrtc").apply { mkdirs() }
            val dataDir = File(dir, "data").apply { mkdirs() }
            val yaml = File(dir, "client.yaml")
            yaml.writeText(buildClientYaml(c, dataDir.absolutePath))

            started = try {
                val p = ProcessBuilder(bin.absolutePath, yaml.absolutePath)
                    .directory(dir)
                    .redirectErrorStream(true)
                    .start()
                process = p
                drainLogs(p)
                Log.i(TAG, "olcRTC child started; waiting for SOCKS5 :$SOCKS_PORT")
                p
            } catch (e: Exception) {
                Log.e(TAG, "ensureStarted: exec failed: ${e.message}", e)
                return false
            }
        }
        // Poll the port OUTSIDE the lock so stop() can still interrupt a stuck start.
        val deadline = System.currentTimeMillis() + READY_TIMEOUT_MS
        while (System.currentTimeMillis() < deadline) {
            if (!alive(started)) {
                Log.e(TAG, "olcRTC child exited early (code=${runCatching { started.exitValue() }.getOrNull()}) — see olcRTC logs above")
                return false
            }
            if (portOpen()) {
                // Re-check under the lock that OUR child is still the live one — stop()/a
                // respawn could have raced in while we were polling the open port.
                synchronized(lock) {
                    if (process === started && alive(started)) {
                        Log.i(TAG, "olcRTC SOCKS5 ready")
                        return true
                    }
                }
                return false
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

    /**
     * Kill any orphan libolcrtc.so processes left under our UID (we exec the child; if the app
     * process is OS-killed the child is reparented to init and keeps holding :8808). Scans /proc
     * for our own binary path and SIGKILLs matches. Same-UID only, so this can't touch anything
     * but our own leaked children.
     */
    private fun reapStaleChildren() {
        val bin = binaryFile()?.absolutePath ?: return
        val me = android.os.Process.myPid()
        runCatching {
            File("/proc").listFiles { f -> f.isDirectory && f.name.all(Char::isDigit) }?.forEach { d ->
                val pid = d.name.toIntOrNull() ?: return@forEach
                if (pid == me) return@forEach
                val cmdline = runCatching { File(d, "cmdline").readText() }.getOrNull() ?: return@forEach
                if (cmdline.contains(bin)) {
                    Log.w(TAG, "reaping orphan olcRTC child pid=$pid")
                    runCatching { android.os.Process.killProcess(pid) }
                }
            }
        }
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

    private val secretRe = Regex("[0-9a-fA-F]{16,}")

    private fun drainLogs(p: Process) {
        Thread({
            runCatching {
                // Redact long hex runs (the 64-hex shared key) so it can't land in logcat /
                // the crash-telemetry log.
                p.inputStream.bufferedReader().forEachLine { line ->
                    Log.i("olcRTC", secretRe.replace(line) { "<redacted:${it.value.length}>" })
                }
            }
        }, "olcrtc-logs").apply { isDaemon = true; start() }
    }

    /**
     * The cnc-mode client config (mirrors olcrtc's telemost+vp8channel client example). The DNS is
     * set to a RU resolver (77.88.8.8), but note sing-box's hijack-dns intercepts the child's :53
     * traffic anyway — the actual loop-avoidance for the SFU lookup is the backend DNS rule
     * (carrier domains → local resolver), not this value. room/key are validated in setCreds and
     * quoted here (room is a URL).
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

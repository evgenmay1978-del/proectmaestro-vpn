package com.maestrovpn.tv.whitelist

import android.os.SystemClock
import android.util.Log
import com.maestrovpn.tv.Application
import android.os.Build
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Child-process runner for the authenticated XHTTP client.
 *
 * The in-process C-shared library (the retired JNI bridge) crashed the app with SIGSEGV on the
 * first CDN session start: libbox.so (go1.25.11) and libmaestro_xhttp.so (go1.26.7) are two Go
 * runtimes in one address space — the coexistence android-xhttp/README.md always described as an
 * unproven device gate (measured 2026-09-26 on the owner phone, reproduced both with the shipped
 * library and with a -Wl,-Bsymbolic rebuild). The engine now ships as a separate PIE executable
 * in jniLibs (useLegacyPackaging=true extracts it to nativeLibraryDir), the same pattern the app
 * used for olcRTC/WDTT.
 *
 * Socket bypass: a child process cannot call VpnService.protect(), so the child own sockets to
 * the CDN edge are kept out of the tun by the DIRECT rule that WhiteListConfig.inject emits for
 * the resolved edge address. Nothing else about the session lifecycle changes.
 *
 * Status codes are the ones the JNI ABI used, so callers keep their meaning:
 * 0 ok, 1 invalid input, 2 busy, 3 stale/reused session, 4 start failure, 5 setup failure,
 * 6 stop failure.
 */
internal object XhttpProcess {
    private const val TAG = "XhttpProcess"
    private const val READY_TIMEOUT_MS = 15_000L
    private const val READY_POLL_MS = 100L
    private const val STOP_TIMEOUT_MS = 3_000L
    private const val MAX_PAYLOAD_BYTES = 16 * 1024

    private const val STATUS_OK = 0
    private const val STATUS_INVALID_INPUT = 1
    private const val STATUS_BUSY = 2
    private const val STATUS_STALE_SESSION = 3
    private const val STATUS_START_FAILED = 4
    private const val STATUS_SETUP_FAILED = 5
    private const val STATUS_STOP_FAILED = 6

    private var child: Process? = null
    private var session = 0L
    private var payload: File? = null

    private fun binary(): File? = runCatching {
        File(Application.application.applicationInfo.nativeLibraryDir, "libmaestro_xhttp.so")
    }.getOrNull()?.takeIf { it.isFile && it.canExecute() }

    fun available(): Boolean = binary() != null

    @Synchronized
    fun start(id: Long, bytes: ByteArray): Int {
        if (id <= 0 || id > Int.MAX_VALUE || bytes.isEmpty() || bytes.size > MAX_PAYLOAD_BYTES) {
            Log.e(TAG, "invalid input id=" + id + " bytes=" + bytes.size)
            return STATUS_INVALID_INPUT
        }
        if (child != null) {
            Log.e(TAG, "busy id=" + id + " running=" + session)
            return STATUS_BUSY
        }
        val bin = binary()
        if (bin == null) {
            Log.e(TAG, "child binary missing under " + Application.application.applicationInfo.nativeLibraryDir)
            return STATUS_SETUP_FAILED
        }
        Log.d(TAG, "start id=" + id + " bytes=" + bytes.size + " bin=" + bin.absolutePath)
        val file = File(Application.application.filesDir, "xhttp-payload.json")
        var started: Process? = null
        return try {
            file.writeBytes(bytes)
            file.setReadable(false, false)
            file.setReadable(true, true)
            started = ProcessBuilder(
                bin.absolutePath, "-payload", file.absolutePath, "-session", id.toString(),
            ).redirectErrorStream(true).start()
            if (ready(started)) {
                child = started
                session = id
                payload = file
                Log.d(TAG, "ready id=" + id + " pid=" + runCatching { started.pid() }.getOrNull())
                STATUS_OK
            } else {
                kill(started)
                Log.e(TAG, "child did not report ready")
                STATUS_START_FAILED
            }
        } catch (e: Exception) {
            started?.let { kill(it) }
            Log.e(TAG, "start failed: " + e.message)
            STATUS_START_FAILED
        } finally {
            runCatching { file.delete() }
        }
    }

    // minSdk 23 — Process.isAlive()/waitFor(timeout)/destroyForcibly() are API 26+, so liveness is
    // probed the portable way the other child-process managers in this app use: exitValue() throws
    // while the child still runs.
    private fun alive(running: Process): Boolean =
        try { running.exitValue(); false } catch (_: IllegalThreadStateException) { true }

    private fun kill(running: Process) {
        runCatching { running.destroy() }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) runCatching { running.destroyForcibly() }
    }

    /** SIGTERM (the child stops the engine and exits); SIGKILL only if it overstays. */
    @Synchronized
    fun stop(id: Long): Int {
        val running = child ?: return STATUS_OK
        if (id != session) return STATUS_STALE_SESSION
        child = null
        session = 0L
        payload = null
        return try {
            running.destroy()
            val deadline = SystemClock.elapsedRealtime() + STOP_TIMEOUT_MS
            while (alive(running) && SystemClock.elapsedRealtime() < deadline) Thread.sleep(READY_POLL_MS)
            if (alive(running)) kill(running)
            STATUS_OK
        } catch (e: Exception) {
            Log.e(TAG, "stop failed: " + e.message)
            STATUS_STOP_FAILED
        }
    }

    /** Waits for the child "ready" line; drains stdout so the pipe cannot fill up. */
    private fun ready(running: Process): Boolean {
        val seen = AtomicBoolean(false)
        val drain = Thread {
            runCatching {
                running.inputStream.bufferedReader().forEachLine { line ->
                    if (line.trim() == "ready") seen.set(true) else Log.d(TAG, "child: " + line)
                }
            }
        }
        drain.isDaemon = true
        drain.start()
        val deadline = SystemClock.elapsedRealtime() + READY_TIMEOUT_MS
        while (SystemClock.elapsedRealtime() < deadline) {
            if (seen.get()) return true
            if (!alive(running)) return false
            Thread.sleep(READY_POLL_MS)
        }
        return seen.get()
    }
}

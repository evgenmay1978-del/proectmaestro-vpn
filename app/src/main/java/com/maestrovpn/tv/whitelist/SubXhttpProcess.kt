package com.maestrovpn.tv.whitelist

import android.os.Build
import android.os.SystemClock
import android.util.Log
import com.maestrovpn.tv.Application
import java.io.File
import java.net.InetSocketAddress
import java.net.Socket
import java.util.ArrayDeque

/**
 * Child-process runner for xhttp subscription nodes (see [SubXhttpRuntime]).
 *
 * The engine ships as a separate PIE executable in jniLibs (official Xray-core, built for Android
 * in CI) and is started exactly like the CDN runner: a sibling process, never a second Go runtime
 * inside libbox (that combination SIGSEGVs — measured 2026-09-26).
 *
 * A child process cannot call VpnService.protect(), so its TCP to the entry node is kept out of the
 * tun by the DIRECT rule that [SubXhttpRuntime.patchDirectRules] emits for the node IPs.
 */
internal object SubXhttpProcess {
    private const val TAG = "SubXhttpProcess"
    private const val BINARY = "libmaestro_subxhttp.so"
    private const val READY_TIMEOUT_MS = 8_000L
    private const val READY_POLL_MS = 100L
    private const val STOP_TIMEOUT_MS = 3_000L
    private const val CONNECT_TIMEOUT_MS = 300
    private const val LOG_KEEP = 40

    private var child: Process? = null
    private var activeTag: String? = null
    private var configFile: File? = null
    private val log = ArrayDeque<String>()

    fun available(): Boolean = binary() != null

    fun runningTag(): String? = activeTag?.takeIf { child?.let { proc -> alive(proc) } == true }

    private fun binary(): File? = runCatching {
        File(Application.application.applicationInfo.nativeLibraryDir, BINARY)
    }.getOrNull()?.takeIf { it.isFile && it.canExecute() }

    private fun logTail(): String = synchronized(log) { log.joinToString(" | ") }

    @Synchronized
    fun start(node: SubXhttpNode, address: String): Boolean {
        if (activeTag == node.tag && child?.let { alive(it) } == true && reachable(node.socksPort)) return true
        stopLocked()
        val bin = binary()
        if (bin == null) {
            Log.w(TAG, "child binary missing in " + Application.application.applicationInfo.nativeLibraryDir)
            return false
        }
        val file = File(Application.application.filesDir, "subxhttp-" + node.socksPort + ".json")
        var started: Process? = null
        return try {
            file.writeText(SubXhttpRuntime.xrayConfig(node, address))
            file.setReadable(false, false)
            file.setReadable(true, true)
            started = ProcessBuilder(bin.absolutePath, "run", "-c", file.absolutePath)
                .redirectErrorStream(true)
                .start()
            drain(started)
            val ready = awaitReady(started, node.socksPort)
            if (ready) {
                child = started
                activeTag = node.tag
                configFile = file
                Log.d(TAG, "ready tag=" + node.tag + " port=" + node.socksPort)
                true
            } else {
                Log.e(TAG, "child not ready: " + logTail())
                runCatching { file.delete() }
                kill(started)
                false
            }
        } catch (e: Exception) {
            Log.e(TAG, "start failed: " + e.message)
            runCatching { file.delete() }
            started?.let { kill(it) }
            false
        }
    }

    @Synchronized
    fun stop() {
        stopLocked()
    }

    private fun stopLocked() {
        val running = child ?: run {
            activeTag = null
            configFile?.let { runCatching { it.delete() } }
            configFile = null
            return
        }
        child = null
        activeTag = null
        runCatching {
            running.destroy()
            val deadline = SystemClock.elapsedRealtime() + STOP_TIMEOUT_MS
            while (alive(running) && SystemClock.elapsedRealtime() < deadline) Thread.sleep(READY_POLL_MS)
            if (alive(running)) kill(running)
        }
        configFile?.let { runCatching { it.delete() } }
        configFile = null
    }

    private fun alive(running: Process): Boolean =
        try { running.exitValue(); false } catch (_: IllegalThreadStateException) { true }

    private fun kill(running: Process) {
        runCatching { running.destroy() }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) runCatching { running.destroyForcibly() }
    }

    private fun drain(running: Process) {
        val thread = Thread {
            runCatching {
                running.inputStream.bufferedReader().forEachLine { line ->
                    Log.d(TAG, "child: " + line)
                    synchronized(log) {
                        log.addLast(line.take(200))
                        while (log.size > LOG_KEEP) log.removeFirst()
                    }
                }
            }
        }
        thread.isDaemon = true
        thread.start()
    }

    private fun awaitReady(running: Process, port: Int): Boolean {
        val deadline = SystemClock.elapsedRealtime() + READY_TIMEOUT_MS
        while (SystemClock.elapsedRealtime() < deadline) {
            if (reachable(port)) return true
            if (!alive(running)) return false
            Thread.sleep(READY_POLL_MS)
        }
        return reachable(port)
    }

    private fun reachable(port: Int): Boolean = runCatching {
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", port), CONNECT_TIMEOUT_MS)
            true
        }
    }.getOrDefault(false)
}

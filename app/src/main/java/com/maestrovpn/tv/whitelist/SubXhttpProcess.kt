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
    private const val LOG_MAX_BYTES = 64L * 1024
    private const val ALL_TAG = "auto:all"
    private val VERIFY_TARGETS = listOf("api.ipify.org" to 80, "cp.cloudflare.com" to 80)

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

    /**
     * Plain append-only file in filesDir so a "node does not work" report can be diagnosed after
     * the fact: logcat is not readable from the app itself and Log.* is wiped on restart.
     */
    fun fileLog(message: String) {
        // Two sinks on purpose: the internal file is what a bug report can carry, the external one
        // is the only copy a shell can read on a release build (internal paths are refused), and
        // this runner has to be diagnosable from the phone itself (14.11.2026).
        val line = System.currentTimeMillis().toString() + " " + message + "\n"
        val dirs = listOfNotNull(Application.application.filesDir, Application.application.getExternalFilesDir(null))
        for (dir in dirs) {
            runCatching {
                val file = File(dir, "subxhttp.log")
                // Rotate, never drop. On 14.11.2026 the file was deleted the moment it crossed
                // 64 KB, and the stream of "accepted tcp:" access lines rotated away every single
                // "verify ... Read timed out" line — the report that needed the log had no log.
                if (file.isFile && file.length() > LOG_MAX_BYTES) {
                    val prev = File(dir, "subxhttp.log.1")
                    runCatching { prev.delete() }
                    runCatching { file.renameTo(prev) }
                }
                file.appendText(line)
            }
        }
    }

    /**
     * Start the runner for [node] and adopt it only after it proves it carries traffic.
     *
     * The previous runner is retired only once the new one has answered a real request through its
     * own SOCKS port. That ordering is deliberate: stopping first would leave the engine routing
     * into a closed port whenever the new node turns out to be broken (measured on the German
     * Akonit node, 14.11.2026, which accepted TCP but never passed a byte). Both runners use
     * different ports, so they can coexist for the few seconds the check takes.
     */
    @Synchronized
    fun start(node: SubXhttpNode, address: String, port: Int = node.socksPort): Boolean {
        if (activeTag == node.tag && child?.let { alive(it) } == true) {
            if (reachable(port) && verifyTunnel(port)) return true
            fileLog("current " + node.tag + " failed verification -> restarting port=" + port)
            stopLocked()
        }
        val candidate = spawn(node, address, port) ?: return false
        if (!verifyTunnel(port)) {
            fileLog(
                "candidate " + node.tag + " failed verification -> previous runner kept addr=" + address +
                    " direct=" + SubXhttpRuntime.ruleApplied(address),
            )
            runCatching { candidate.file.delete() }
            kill(candidate.process)
            return false
        }
        // Proven: retire the old runner now, and only now.
        stopLocked()
        child = candidate.process
        activeTag = node.tag
        configFile = candidate.file
        Log.d(TAG, "ready tag=" + node.tag + " port=" + port)
        fileLog(
            "ready tag=" + node.tag + " port=" + port + " addr=" + address +
                " direct=" + SubXhttpRuntime.ruleApplied(address),
        )
        return true
    }

    /**
     * Start ONE runner process that carries every xhttp node at once. The engine's "auto" urltest
     * then has a live local port for each node; the same runner also serves a manual xhttp pick.
     * Returns true only when the process is alive and every expected port is listening.
     */
    @Synchronized
    fun startAll(nodes: List<SubXhttpNode>, ports: Map<String, Int>): Boolean {
        val entries = nodes.mapNotNull { node -> ports[node.tag]?.let { node to it } }
        if (entries.isEmpty()) {
            fileLog("startAll skipped: no nodes with profile ports")
            return false
        }
        val expected = entries.map { it.second }
        if (activeTag == ALL_TAG && child?.let { alive(it) } == true && expected.all { reachable(it) }) {
            return true
        }
        // The new all-node process binds the SAME profile ports as a running single-node child.
        // Start it only after the old one is gone; otherwise the bind fails and auto loses every
        // xhttp port for no reason.
        if (child != null) {
            if (alive(child!!)) fileLog("all runner replaces tag=" + activeTag)
            stopLocked()
        }
        val addresses = SubXhttpRuntime.resolveAddresses(entries.map { it.first })
        val candidate = spawnAll(entries, addresses) ?: return false
        child = candidate.process
        activeTag = ALL_TAG
        configFile = candidate.file
        fileLog("ready " + ALL_TAG + " nodes=" + entries.size + " ports=" + expected)
        return true
    }

    private fun spawnAll(
        entries: List<Pair<SubXhttpNode, Int>>,
        addresses: Map<String, String>,
    ): Spawned? {
        val bin = binary()
        if (bin == null) {
            Log.w(TAG, "child binary missing in " + Application.application.applicationInfo.nativeLibraryDir)
            fileLog("startAll failed: binary missing")
            return null
        }
        val file = File(Application.application.filesDir, "subxhttp-all.json")
        var started: Process? = null
        return try {
            file.writeText(SubXhttpRuntime.xrayConfigAll(entries, addresses))
            file.setReadable(false, false)
            file.setReadable(true, true)
            started = ProcessBuilder(bin.absolutePath, "run", "-c", file.absolutePath)
                .redirectErrorStream(true)
                .start()
            drain(started)
            if (!awaitAllReady(started, entries.map { it.second })) {
                Log.e(TAG, "all child not ready: " + logTail())
                fileLog("all not ready nodes=" + entries.size + " tail=" + logTail())
                runCatching { file.delete() }
                kill(started)
                return null
            }
            Spawned(started, file)
        } catch (e: Exception) {
            Log.e(TAG, "startAll failed: " + e.message)
            fileLog("startAll failed: " + e.message)
            runCatching { file.delete() }
            started?.let { kill(it) }
            null
        }
    }

    private fun awaitAllReady(running: Process, ports: List<Int>): Boolean {
        val deadline = SystemClock.elapsedRealtime() + READY_TIMEOUT_MS
        while (SystemClock.elapsedRealtime() < deadline) {
            if (ports.all { reachable(it) }) return true
            if (!alive(running)) return false
            Thread.sleep(READY_POLL_MS)
        }
        return ports.all { reachable(it) }
    }

    private class Spawned(val process: Process, val file: File)

    /** Launch a runner process and wait for its SOCKS port. Does not touch [child]/[activeTag]. */
    private fun spawn(node: SubXhttpNode, address: String, port: Int): Spawned? {
        val bin = binary()
        if (bin == null) {
            Log.w(TAG, "child binary missing in " + Application.application.applicationInfo.nativeLibraryDir)
            fileLog("start failed tag=" + node.tag + ": binary missing")
            return null
        }
        val file = File(Application.application.filesDir, "subxhttp-" + port + ".json")
        var started: Process? = null
        return try {
            file.writeText(SubXhttpRuntime.xrayConfig(node, address, port))
            file.setReadable(false, false)
            file.setReadable(true, true)
            started = ProcessBuilder(bin.absolutePath, "run", "-c", file.absolutePath)
                .redirectErrorStream(true)
                .start()
            drain(started)
            if (!awaitReady(started, port)) {
                Log.e(TAG, "child not ready: " + logTail())
                fileLog("not ready tag=" + node.tag + " port=" + port + " tail=" + logTail())
                runCatching { file.delete() }
                kill(started)
                return null
            }
            Spawned(started, file)
        } catch (e: Exception) {
            Log.e(TAG, "start failed: " + e.message)
            fileLog("start failed tag=" + node.tag + ": " + e.message)
            runCatching { file.delete() }
            started?.let { kill(it) }
            null
        }
    }

    @Synchronized
    fun stop() {
        stopLocked()
    }

    private fun stopLocked() {
        if (child != null) fileLog("stopping tag=" + activeTag)
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
                    // Access lines ("accepted tcp:...") are one per connection and would fill the
                    // diagnostic file within seconds; errors and startup lines are what matter.
                    if (line.contains("accepted tcp:") || line.contains("accepted udp:")) {
                        return@forEachLine
                    }
                    Log.d(TAG, "child: " + line)
                    fileLog("child: " + line.take(200))
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

    /**
     * End-to-end check that this runner really carries traffic, not just that its SOCKS port is
     * open. A node can accept the TCP handshake on its entry and still drop every byte inside
     * (measured on the German Akonit node 14.11.2026: 6 connections accepted, no response), so
     * probing the entry is not enough to decide whether the phone may be routed into it.
     *
     * Two plain-HTTP targets are used on purpose: one failing host must not condemn a working node.
     */
    fun verifyTunnel(port: Int, timeoutMs: Long = 9_000L): Boolean {
        val deadline = SystemClock.elapsedRealtime() + timeoutMs
        for (target in VERIFY_TARGETS) {
            val left = deadline - SystemClock.elapsedRealtime()
            if (left < 800L) break
            if (httpViaSocks(port, target.first, target.second, left)) {
                fileLog("verify port=" + port + " ok via " + target.first)
                return true
            }
        }
        fileLog("verify port=" + port + " failed")
        return false
    }

    private fun httpViaSocks(port: Int, host: String, httpPort: Int, budgetMs: Long): Boolean = try {
        Socket().use { socket ->
            socket.connect(InetSocketAddress("127.0.0.1", port), CONNECT_TIMEOUT_MS)
            socket.soTimeout = budgetMs.coerceAtMost(4_500L).toInt()
            val out = socket.getOutputStream()
            val inp = socket.getInputStream()
            // SOCKS5 greeting: version 5, one method, "no auth".
            out.write(byteArrayOf(5, 1, 0))
            out.flush()
            val greeting = ByteArray(2)
            readFully(inp, greeting)
            if (greeting[0].toInt() != 5 || greeting[1].toInt() != 0) return false
            val name = host.toByteArray(Charsets.US_ASCII)
            val request = ByteArray(5 + name.size + 2)
            request[0] = 5; request[1] = 1; request[2] = 0; request[3] = 3; request[4] = name.size.toByte()
            System.arraycopy(name, 0, request, 5, name.size)
            request[5 + name.size] = ((httpPort shr 8) and 0xFF).toByte()
            request[6 + name.size] = (httpPort and 0xFF).toByte()
            out.write(request)
            out.flush()
            val reply = ByteArray(4)
            readFully(inp, reply)
            if (reply[0].toInt() != 5 || reply[1].toInt() != 0) return false
            when (reply[3].toInt()) {
                1 -> readFully(inp, ByteArray(6))
                4 -> readFully(inp, ByteArray(18))
                3 -> {
                    val size = ByteArray(1)
                    readFully(inp, size)
                    readFully(inp, ByteArray(size[0].toInt() + 2))
                }
                else -> return false
            }
            // The SOCKS handshake alone only proves the runner accepted us; the HTTP answer proves
            // the node actually reached the internet through its own tunnel.
            out.write(("GET / HTTP/1.0\r\nHost: " + host + "\r\nConnection: close\r\n\r\n").toByteArray())
            out.flush()
            val head = ByteArray(64)
            val read = inp.read(head)
            if (read <= 0) return false
            String(head, 0, read).startsWith("HTTP/1.")
        }
    } catch (e: Exception) {
        fileLog("verify " + host + " error: " + e.message)
        false
    }

    private fun readFully(input: java.io.InputStream, buffer: ByteArray) {
        var offset = 0
        while (offset < buffer.size) {
            val read = input.read(buffer, offset, buffer.size - offset)
            if (read <= 0) throw java.io.EOFException("short read")
            offset += read
        }
    }
}

package com.maestrovpn.tv.bg

import android.content.Context
import android.os.Build
import android.util.Log
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.utils.DeviceFormFactor
import com.maestrovpn.tv.utils.MaestroSub
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** Lifecycle for the optional WDTT transport child. It never owns an Android VPN interface. */
object WdttManager {
    const val OUTBOUND_TAG = "vk-turn"
    const val LISTEN_ADDRESS = "127.0.0.1:9000"

    private const val TAG = "WdttManager"
    private const val PREFS = "maestro_wdtt"
    private const val READY_TIMEOUT_MS = 30_000L

    internal data class Creds(
        val peer: String,
        val vkHashes: List<String>,
        val password: String,
        val workers: Int,
        val fingerprint: String,
        val clientIds: List<String>,
        val obfsMode: String,
    )

    @Volatile private var creds: Creds? = null
    @Volatile private var loaded = false
    @Volatile private var process: Process? = null
    @Volatile private var startedWith: Creds? = null
    @Volatile private var starting = false
    private var stopEpoch = 0L
    private val lock = Any()

    fun setCreds(
        peer: String?,
        vkHashes: List<String>?,
        password: String?,
        workers: Int?,
        fingerprint: String?,
        clientIds: List<String>?,
        obfsMode: String?,
    ) {
        val candidate = validateCreds(peer, vkHashes, password, workers, fingerprint, clientIds, obfsMode)
        // TV must never retain credentials which could unlock or bootstrap this transport later.
        val app = Application.application
        val mobileCandidate = candidate.takeUnless { DeviceFormFactor.isTelevision(app) }
        creds = mobileCandidate
        loaded = true
        runCatching {
            val editor = prefs().edit()
            if (mobileCandidate == null) {
                editor.clear()
            } else {
                editor.putString("peer", mobileCandidate.peer)
                    .putString("hashes", mobileCandidate.vkHashes.joinToString(","))
                    .putString("password", mobileCandidate.password)
                    .putInt("workers", mobileCandidate.workers)
                    .putString("fingerprint", mobileCandidate.fingerprint)
                    .putString("client_ids", mobileCandidate.clientIds.joinToString(","))
                    .putString("obfs_mode", mobileCandidate.obfsMode)
            }
            editor.apply()
        }
    }

    fun hasCreds(): Boolean {
        ensureLoaded()
        return creds != null
    }

    fun isUnlocked(): Boolean {
        ensureLoaded()
        val app = Application.application
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.P &&
            !DeviceFormFactor.isTelevision(app) && creds != null && binaryFile()?.exists() == true
    }

    val isRunning: Boolean get() = process?.let(::alive) == true

    /** Blocks off-main-thread until the child emits a structured READY event on stdout. */
    fun ensureStarted(): Boolean {
        val app = Application.application
        // Runtime hard gate immediately before any spawn path; UI/server gating is not trusted.
        if (DeviceFormFactor.isTelevision(app)) {
            Log.w(TAG, "WDTT is disabled on television devices")
            stop()
            return false
        }
        // The pinned upstream child is linked against Android API 28. Fail closed on older phones.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
            Log.w(TAG, "WDTT requires Android 9 (API 28) or newer")
            stop()
            return false
        }
        ensureLoaded()

        val epoch: Long
        synchronized(lock) {
            val current = creds ?: return false
            if (isRunning && startedWith == current) return true
            if (starting) return false
            starting = true
            epoch = stopEpoch
        }

        try {
            val child: Process
            val ready = CountDownLatch(1)
            synchronized(lock) {
                val current = creds ?: return false
                stopLocked()
                reapOrphans()
                val binary = binaryFile()?.takeIf(File::exists) ?: return false

                // Repeat the hard gate in the same critical section as ProcessBuilder.start().
                if (DeviceFormFactor.isTelevision(app)) return false
                child = try {
                    ProcessBuilder(
                        binary.absolutePath,
                        "-peer", current.peer,
                        "-vk", current.vkHashes.joinToString(","),
                        "-n", current.workers.toString(),
                        "-listen", LISTEN_ADDRESS,
                        "-fingerprint", current.fingerprint,
                        "-client-ids", current.clientIds.joinToString(","),
                        "-device-id", MaestroSub.deviceId(app),
                        "-password", current.password,
                        "-captcha-mode", "auto",
                        "-vk-auth-mode", "vkcalls",
                        "-obfs", current.obfsMode,
                    )
                        .directory(app.filesDir)
                        .redirectErrorStream(true)
                        .apply {
                            environment()["WDTT_EVENTS"] = "1"
                        }
                        .start()
                } catch (e: Exception) {
                    Log.e(TAG, "WDTT exec failed: ${e.message}", e)
                    return false
                }
                process = child
                startedWith = current
                drainEvents(child, ready)
            }

            val signalled = ready.await(READY_TIMEOUT_MS, TimeUnit.MILLISECONDS)
            synchronized(lock) {
                if (signalled && epoch == stopEpoch && process === child && alive(child)) return true
                if (process === child) stopLocked()
            }
            return false
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            return false
        } finally {
            synchronized(lock) { starting = false }
        }
    }

    fun stop() = synchronized(lock) {
        stopEpoch++
        stopLocked()
    }

    private fun stopLocked() {
        val child = process
        process = null
        startedWith = null
        if (child == null) return
        runCatching {
            // Upstream supports a graceful stdin control protocol; let workers tear down first.
            child.outputStream.bufferedWriter().use { writer ->
                writer.write("STOP\n")
                writer.flush()
            }
            child.destroy()
            val deadline = System.currentTimeMillis() + 2_000L
            while (alive(child) && System.currentTimeMillis() < deadline) Thread.sleep(50L)
            if (alive(child) && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) child.destroyForcibly()
        }
    }

    private fun drainEvents(child: Process, ready: CountDownLatch) {
        Thread({
            try {
                child.inputStream.bufferedReader().forEachLine { line ->
                    if (isReadyEvent(line)) ready.countDown()
                    Log.i("WDTT", redact(line))
                }
            } catch (e: Exception) {
                Log.w(TAG, "WDTT event stream closed: ${e.message}")
            } finally {
                // Wake ensureStarted immediately on early exit; its liveness check rejects it.
                ready.countDown()
            }
        }, "wdtt-events").apply { isDaemon = true; start() }
    }

    private fun reapOrphans() {
        val binary = binaryFile()?.absolutePath ?: return
        val ownPid = android.os.Process.myPid()
        runCatching {
            File("/proc").listFiles { f -> f.isDirectory && f.name.all(Char::isDigit) }?.forEach { dir ->
                val pid = dir.name.toIntOrNull() ?: return@forEach
                if (pid == ownPid) return@forEach
                val cmdline = runCatching { File(dir, "cmdline").readText() }.getOrNull() ?: return@forEach
                if (cmdline.contains(binary)) android.os.Process.killProcess(pid)
            }
        }
    }

    private fun ensureLoaded() {
        if (loaded) return
        synchronized(lock) {
            if (loaded) return
            val app = Application.application
            if (DeviceFormFactor.isTelevision(app)) {
                runCatching { prefs().edit().clear().apply() }
                creds = null
                loaded = true
                return
            }
            runCatching {
                val p = prefs()
                creds = validateCreds(
                    p.getString("peer", null),
                    p.getString("hashes", null)?.split(','),
                    p.getString("password", null),
                    if (p.contains("workers")) p.getInt("workers", 0) else null,
                    p.getString("fingerprint", null),
                    p.getString("client_ids", null)?.split(','),
                    p.getString("obfs_mode", null),
                )
            }
            loaded = true
        }
    }

    private fun prefs() = Application.application.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
    private fun binaryFile() = runCatching {
        File(Application.application.applicationInfo.nativeLibraryDir, "libwdtt.so")
    }.getOrNull()

    private fun alive(child: Process): Boolean =
        try { child.exitValue(); false } catch (_: IllegalThreadStateException) { true }

    internal fun isReadyEvent(line: String): Boolean =
        line.startsWith("__WDTT_EVENT__|READY|")

    private fun redact(line: String): String = line
        .replace(Regex("(?i)(password|token|secret|key)(\\s*[=:]\\s*)[^,\\s\"}]+"), "$1$2<redacted>")

    internal fun validateCreds(
        peer: String?, hashes: List<String>?, password: String?, workers: Int?, fingerprint: String?,
        clientIds: List<String>?, obfsMode: String?,
    ): Creds? {
        val p = peer?.trim().orEmpty()
        val hs = hashes?.map(String::trim)?.filter(String::isNotEmpty).orEmpty()
        val pass = password?.trim().orEmpty()
        val fp = fingerprint?.trim().orEmpty()
        val ids = clientIds?.map(String::trim)?.filter(String::isNotEmpty).orEmpty()
        val obfs = obfsMode?.trim().orEmpty()
        val safeToken = Regex("^[A-Za-z0-9._~-]{1,160}$")
        val peerRe = Regex("^(?:[A-Za-z0-9.-]+|\\[[0-9A-Fa-f:]+]):[1-9][0-9]{0,4}$")
        if (!p.matches(peerRe) || hs.isEmpty() || hs.size > 4 || hs.any { !it.matches(safeToken) }) return null
        if (pass.length !in 8..128 || pass.any { it.code < 0x21 || it.code > 0x7e }) return null
        val workerCount = workers ?: return null
        // Upstream allocates workers in groups of nine (one VK call allocation per group).
        if (workerCount !in 9..108 || workerCount % 9 != 0 || !fp.matches(safeToken) || ids.isEmpty() || ids.size > 8) return null
        if (ids.any { !it.matches(Regex("^[0-9]{1,20}$")) } || !obfs.matches(Regex("^[a-z0-9_-]{1,32}$"))) return null
        val port = p.substringAfterLast(':').toIntOrNull() ?: return null
        if (port !in 1..65535) return null
        return Creds(p, hs, pass, workerCount, fp, ids, obfs)
    }
}

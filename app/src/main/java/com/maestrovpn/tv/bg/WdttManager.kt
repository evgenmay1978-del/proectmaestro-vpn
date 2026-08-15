package com.maestrovpn.tv.bg

import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.SystemClock
import android.util.Log
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.compose.wdtt.WdttCaptchaActivity
import com.maestrovpn.tv.compose.wdtt.WdttCaptchaPolicy
import com.maestrovpn.tv.compose.wdtt.WdttCaptchaResult
import com.maestrovpn.tv.utils.DeviceFormFactor
import com.maestrovpn.tv.utils.MaestroSub
import java.io.File
import java.io.Writer
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

internal fun canSpawnWdtt(
    isTelevision: Boolean,
    sdkInt: Int,
    hasCreds: Boolean,
    binaryExists: Boolean,
): Boolean = !isTelevision && sdkInt >= Build.VERSION_CODES.P && hasCreds && binaryExists

internal class WdttCommandWriter(private val sink: Writer) {
    @Synchronized
    fun writeCaptchaResult(result: WdttCaptchaResult): Boolean {
        val value = when (result) {
            is WdttCaptchaResult.Success ->
                WdttCaptchaPolicy.sanitizeSuccessToken(result.token) ?: return false
            WdttCaptchaResult.Cancelled -> "error:cancelled"
            WdttCaptchaResult.Timeout -> "error:timeout"
        }
        return writeLine("CAPTCHA_RESULT|$value")
    }

    @Synchronized
    fun writeStop(): Boolean = writeLine("STOP")

    @Synchronized
    private fun writeLine(value: String): Boolean = runCatching {
        sink.write(value)
        sink.write("\n")
        sink.flush()
    }.isSuccess

    @Synchronized
    fun close() = runCatching { sink.close() }.getOrNull()
}

internal class WdttCaptchaExchange(private val writer: WdttCommandWriter) {
    private var sequence = 0L
    private var active: Long? = null

    @Synchronized
    fun open(request: WdttCaptchaRequest): Long {
        check(request.redirectUri.isNotBlank())
        sequence = if (sequence == Long.MAX_VALUE) 1L else sequence + 1L
        active = sequence
        return sequence
    }

    @Synchronized
    fun submit(requestId: Long, result: WdttCaptchaResult): Boolean {
        if (active != requestId) return false
        active = null
        return writer.writeCaptchaResult(result)
    }

    @Synchronized
    fun invalidate() {
        active = null
    }
}

internal data class WdttPublicState(
    val stage: WdttStage,
    val error: WdttSafeErrorCode? = null,
    val captchaPending: Boolean = false,
)

/** Lifecycle for the optional WDTT transport child. It never owns an Android VPN interface. */
object WdttManager {
    const val OUTBOUND_TAG = "vk-turn"
    const val LISTEN_ADDRESS = "127.0.0.1:9000"

    private const val TAG = "WdttManager"
    private const val PREFS = "maestro_wdtt"
    private const val ORDINARY_TIMEOUT_MS = 45_000L
    private const val CAPTCHA_TIMEOUT_MS = 120_000L
    private const val WAIT_SLICE_MS = 50L

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
    @Volatile private var startState: WdttStartState = WdttStartState.Stopped(0L)
    @Volatile private var activeCaptchaRequestId: Long? = null
    private var commandWriter: WdttCommandWriter? = null
    private var captchaExchange: WdttCaptchaExchange? = null
    private var stopEpoch = 0L
    private val lock = Any()
    private val mutablePublicState = MutableStateFlow(WdttPublicState(WdttStage.STOPPED))
    internal val publicState: StateFlow<WdttPublicState> = mutablePublicState.asStateFlow()

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
        return canSpawnWdtt(
            isTelevision = DeviceFormFactor.isTelevision(app),
            sdkInt = Build.VERSION.SDK_INT,
            hasCreds = creds != null,
            binaryExists = binaryFile()?.exists() == true,
        )
    }

    val isRunning: Boolean get() = process?.let(::alive) == true

    /** Blocks off-main-thread until the child emits a structured READY event on stdout. */
    fun ensureStarted(): Boolean {
        val app = Application.application
        ensureLoaded()
        val binary = binaryFile()
        if (!canSpawnWdtt(
                isTelevision = DeviceFormFactor.isTelevision(app),
                sdkInt = Build.VERSION.SDK_INT,
                hasCreds = creds != null,
                binaryExists = binary?.exists() == true,
            )
        ) {
            Log.w(TAG, "WDTT spawn policy denied")
            stop()
            return false
        }

        val epoch: Long
        synchronized(lock) {
            val current = creds ?: return false
            if (isRunning && startedWith == current && startState is WdttStartState.Ready) return true
            if (starting) return false
            starting = true
            epoch = stopEpoch
        }

        try {
            val child: Process
            val startedAt = SystemClock.elapsedRealtime()
            synchronized(lock) {
                val current = creds ?: return false
                stopLocked()
                reapOrphans()
                val executable = binaryFile()?.takeIf(File::exists) ?: return false
                if (!canSpawnWdtt(
                        isTelevision = DeviceFormFactor.isTelevision(app),
                        sdkInt = Build.VERSION.SDK_INT,
                        hasCreds = true,
                        binaryExists = true,
                    )
                ) return false
                child = try {
                    ProcessBuilder(
                        executable.absolutePath,
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
                        .apply { environment()["WDTT_EVENTS"] = "1" }
                        .start()
                } catch (_: Exception) {
                    publish(WdttStage.FAILED, WdttSafeErrorCode.INTERNAL)
                    Log.e(TAG, "WDTT exec failed")
                    return false
                }
                val writer = WdttCommandWriter(child.outputStream.bufferedWriter())
                commandWriter = writer
                captchaExchange = WdttCaptchaExchange(writer)
                activeCaptchaRequestId = null
                process = child
                startedWith = current
                startState = WdttStartState.Waiting(startedAt, WdttStage.STARTING)
                publish(WdttStage.STARTING)
                drainEvents(child, startedAt)
            }

            while (true) {
                val snapshot = startState
                val decision = nextWdttStartDecision(
                    state = snapshot,
                    childAlive = alive(child),
                    nowMs = SystemClock.elapsedRealtime(),
                    ordinaryDeadlineMs = ORDINARY_TIMEOUT_MS,
                    captchaDeadlineMs = CAPTCHA_TIMEOUT_MS,
                )
                when (decision) {
                    WdttStartDecision.WAIT -> Thread.sleep(WAIT_SLICE_MS)
                    WdttStartDecision.STARTED -> synchronized(lock) {
                        if (epoch == stopEpoch && process === child && alive(child)) return true
                        if (process === child) stopLocked()
                        return false
                    }
                    WdttStartDecision.STOP -> {
                        if (snapshot is WdttStartState.CaptchaRequired) {
                            activeCaptchaRequestId?.let { submitCaptchaResult(it, WdttCaptchaResult.Timeout) }
                        }
                        synchronized(lock) { if (process === child) stopLocked() }
                        return false
                    }
                }
            }
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            return false
        } finally {
            synchronized(lock) { starting = false }
        }
    }

    internal fun submitCaptchaResult(requestId: Long, result: WdttCaptchaResult): Boolean = synchronized(lock) {
        val exchange = captchaExchange ?: return false
        if (!exchange.submit(requestId, result)) return false
        activeCaptchaRequestId = null
        when (result) {
            is WdttCaptchaResult.Success -> {
                val now = SystemClock.elapsedRealtime()
                startState = WdttStartState.Waiting(now, WdttStage.VK_AUTH)
                publish(WdttStage.VK_AUTH)
            }
            WdttCaptchaResult.Cancelled,
            WdttCaptchaResult.Timeout,
            -> {
                startState = WdttStartState.Failed(
                    SystemClock.elapsedRealtime(),
                    WdttSafeErrorCode.CAPTCHA_REQUIRED,
                )
                publish(WdttStage.FAILED, WdttSafeErrorCode.CAPTCHA_REQUIRED)
            }
        }
        true
    }

    fun stop() = synchronized(lock) {
        stopEpoch++
        startState = WdttStartState.Cancelled(SystemClock.elapsedRealtime())
        stopLocked()
    }

    private fun stopLocked() {
        val child = process
        val writer = commandWriter
        process = null
        startedWith = null
        commandWriter = null
        captchaExchange?.invalidate()
        captchaExchange = null
        activeCaptchaRequestId = null
        if (child != null) {
            writer?.writeStop()
            runCatching {
                child.destroy()
                val deadline = System.currentTimeMillis() + 2_000L
                while (alive(child) && System.currentTimeMillis() < deadline) Thread.sleep(50L)
                if (alive(child) && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) child.destroyForcibly()
            }
        }
        writer?.close()
        startState = WdttStartState.Stopped(SystemClock.elapsedRealtime())
        publish(WdttStage.STOPPED)
    }

    private fun drainEvents(child: Process, startedAt: Long) {
        Thread({
            try {
                child.inputStream.bufferedReader().forEachLine { line ->
                    val event = parseWdttEvent(line) ?: return@forEachLine
                    synchronized(lock) {
                        if (process !== child) return@synchronized
                        when (event) {
                            is WdttEvent.Stage -> {
                                startState = if (event.stage == WdttStage.READY) {
                                    WdttStartState.Ready(startedAt)
                                } else {
                                    WdttStartState.Waiting(startedAt, event.stage)
                                }
                                publish(event.stage)
                            }
                            is WdttEvent.Failure -> {
                                publish(WdttStage.FAILED, event.code)
                                if (event.fatal) startState = WdttStartState.Failed(startedAt, event.code)
                            }
                            is WdttEvent.Captcha -> handleCaptchaLocked(event.request, startedAt)
                        }
                    }
                }
            } catch (_: Exception) {
                Log.w(TAG, "WDTT event stream closed")
            } finally {
                synchronized(lock) {
                    if (process === child && startState !is WdttStartState.Ready) {
                        startState = WdttStartState.Stopped(startedAt)
                        publish(WdttStage.STOPPED)
                    }
                }
            }
        }, "wdtt-events").apply { isDaemon = true; start() }
    }

    private fun handleCaptchaLocked(request: WdttCaptchaRequest, startedAt: Long) {
        val app = Application.application
        if (DeviceFormFactor.isTelevision(app) || !WdttCaptchaPolicy.isAllowedTopLevel(request.redirectUri)) {
            startState = WdttStartState.Failed(startedAt, WdttSafeErrorCode.CAPTCHA_REQUIRED)
            publish(WdttStage.FAILED, WdttSafeErrorCode.CAPTCHA_REQUIRED)
            return
        }
        val exchange = captchaExchange ?: run {
            startState = WdttStartState.Failed(startedAt, WdttSafeErrorCode.INTERNAL)
            publish(WdttStage.FAILED, WdttSafeErrorCode.INTERNAL)
            return
        }
        val requestId = exchange.open(request)
        activeCaptchaRequestId = requestId
        val requestedAt = SystemClock.elapsedRealtime()
        startState = WdttStartState.CaptchaRequired(startedAt, requestedAt, request)
        publish(WdttStage.CAPTCHA_REQUIRED, captchaPending = true)
        val intent = Intent(app, WdttCaptchaActivity::class.java)
            .putExtra(WdttCaptchaActivity.EXTRA_REQUEST_ID, requestId)
            .putExtra(WdttCaptchaActivity.EXTRA_REDIRECT_URI, request.redirectUri)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        runCatching { app.startActivity(intent) }.onFailure {
            submitCaptchaResult(requestId, WdttCaptchaResult.Cancelled)
        }
    }

    private fun publish(
        stage: WdttStage,
        error: WdttSafeErrorCode? = null,
        captchaPending: Boolean = false,
    ) {
        mutablePublicState.value = WdttPublicState(stage, error, captchaPending)
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
        line.startsWith("__WDTT_EVENT__|READY|") ||
            parseWdttEvent(line) == WdttEvent.Stage(WdttStage.READY)

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
        if (workerCount !in 9..108 || workerCount % 9 != 0 || !fp.matches(safeToken) || ids.isEmpty() || ids.size > 8) return null
        if (ids.any { !it.matches(Regex("^[0-9]{1,20}$")) } || !obfs.matches(Regex("^[a-z0-9_-]{1,32}$"))) return null
        val port = p.substringAfterLast(':').toIntOrNull() ?: return null
        if (port !in 1..65535) return null
        return Creds(p, hs, pass, workerCount, fp, ids, obfs)
    }
}

package com.maestrovpn.tv.bg

import android.os.Build
import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.utils.HTTPClient
import com.maestrovpn.tv.utils.MaestroSub
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter
import java.net.HttpURLConnection
import java.net.URL
import java.text.ParseException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone

data class CrashReport(
    val id: String,
    val date: Date,
    val directory: File,
    val isRead: Boolean,
)

data class CrashReportFile(
    val kind: Kind,
    val displayName: String,
    val file: File,
) {
    enum class Kind {
        METADATA,
        GO_LOG,
        JVM_LOG,
        CONFIG,
    }
}

object CrashReportManager {
    private const val METADATA_FILE_NAME = "metadata.json"
    private const val GO_LOG_FILE_NAME = "go.log"
    private const val JVM_LOG_FILE_NAME = "jvm.log"
    private const val CONFIG_FILE_NAME = "configuration.json"
    private const val READ_MARKER_FILE_NAME = ".read"
    private const val CRASH_REPORTS_DIR_NAME = "crash_reports"
    private const val UPLOADED_MARKER_FILE_NAME = ".uploaded"
    private const val PENDING_JVM_CRASH_FILE_NAME = "CrashReport-JVM.log"
    private const val PENDING_JVM_METADATA_FILE_NAME = "CrashReport-JVM-metadata.json"

    private val timestampFormat = SimpleDateFormat("yyyy-MM-dd'T'HH-mm-ss", Locale.US).apply {
        timeZone = TimeZone.getTimeZone("UTC")
    }

    private lateinit var workingDir: File
    private lateinit var baseDir: File

    private val _reports = MutableStateFlow<List<CrashReport>>(emptyList())
    val reports: StateFlow<List<CrashReport>> = _reports
    private val _unreadCount = MutableStateFlow(0)
    val unreadCount: StateFlow<Int> = _unreadCount

    fun install(workingDir: File, baseDir: File) {
        this.workingDir = workingDir
        this.baseDir = baseDir
        archivePendingJvmCrashReport()
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            writePendingJvmCrashReport(thread, throwable)
            previous?.uncaughtException(thread, throwable)
        }
    }

    private fun writePendingJvmCrashReport(thread: Thread, throwable: Throwable) {
        try {
            val writer = StringWriter()
            throwable.printStackTrace(PrintWriter(writer))
            File(workingDir, PENDING_JVM_CRASH_FILE_NAME).writeText(writer.toString())
            val metadata = JSONObject().apply {
                put("source", "Application")
                put("crashedAt", formatTimestampISO8601(Date()))
                put("exceptionName", throwable.javaClass.name)
                put("exceptionReason", throwable.message ?: "")
                put("processName", Application.application.packageName)
                put("appVersion", BuildConfig.VERSION_CODE.toString())
                put("appMarketingVersion", BuildConfig.VERSION_NAME)
                runCatching {
                    put("coreVersion", Libbox.version())
                    put("goVersion", Libbox.goVersion())
                }
            }
            File(workingDir, PENDING_JVM_METADATA_FILE_NAME).writeText(metadata.toString())
        } catch (_: Throwable) {
        }
    }

    suspend fun refresh() = withContext(Dispatchers.IO) {
        val reports = scanCrashReports()
        _reports.value = reports
        _unreadCount.value = reports.count { !it.isRead }
    }

    /**
     * Silently POST any locally-recorded crash reports we haven't shipped yet to the panel's
     * /report sink (BuildConfig.BACKEND_URL), so the fleet's real failures land on S1 without
     * waiting for a customer to complain. PII-free: only metadata.json (exception + versions) and
     * jvm.log (the stack) are sent — NEVER configuration.json, which holds server creds. Strictly
     * best-effort: a per-dir .uploaded marker stops re-sends, the run is capped, and any
     * network/parse error just leaves the report for the next launch. Never throws.
     */
    suspend fun uploadPending() = withContext(Dispatchers.IO) {
        // Also flush any olcRTC cold-start FAIL reports stranded when their tunnel was down.
        runCatching { flushPendingOlcrtc() }
        if (!::workingDir.isInitialized) return@withContext
        val crashReportsDir = File(workingDir, CRASH_REPORTS_DIR_NAME)
        val dirs = crashReportsDir.listFiles { f -> f.isDirectory } ?: return@withContext
        // newest first, capped per run so a backlog can never become a POST storm
        dirs.sortedByDescending { it.lastModified() }.take(20).forEach { dir ->
            if (File(dir, UPLOADED_MARKER_FILE_NAME).exists()) return@forEach
            runCatching {
                if (uploadReportDir(dir)) File(dir, UPLOADED_MARKER_FILE_NAME).createNewFile()
            }
        }
    }

    private fun uploadReportDir(dir: File): Boolean {
        val meta = runCatching { JSONObject(File(dir, METADATA_FILE_NAME).readText()) }.getOrNull()
        val stack = runCatching { File(dir, JVM_LOG_FILE_NAME).readText() }.getOrNull().orEmpty()
        if (meta == null && stack.isBlank()) return true // nothing useful — mark done so we skip it forever
        val exName = meta?.optString("exceptionName").orEmpty()
        val exReason = meta?.optString("exceptionReason").orEmpty()
        val msg = listOf(exName, exReason).filter { it.isNotBlank() }.joinToString(": ").ifBlank { "crash" }
        val payload = JSONObject().apply {
            put("kind", "crash")
            put("v", meta?.optString("appMarketingVersion")?.ifBlank { null } ?: BuildConfig.VERSION_NAME)
            put("vc", meta?.optString("appVersion")?.toIntOrNull() ?: BuildConfig.VERSION_CODE)
            put("device", Build.MANUFACTURER + " " + Build.MODEL)
            put("api", Build.VERSION.SDK_INT)
            put("id", runCatching { MaestroSub.deviceId(Application.application) }.getOrDefault(""))
            put("ts", dir.lastModified())
            put("msg", msg)
            put("stack", if (stack.length > 8000) stack.substring(0, 8000) else stack)
        }
        return postReport(payload)
    }

    /**
     * Ship an olcRTC connect diagnostic to /report (owner-gated feature — fired from the protocol
     * selector after an olcRTC start attempt). Lets us see WHY the video tunnel fails to come up on
     * a real device WITHOUT an in-app log export: the msg says cold-start-vs-switch + OK-vs-FAIL,
     * the stack is the child's recent (already secret-redacted) log tail. PII-free like the crash
     * path; best-effort, never throws.
     */
    suspend fun reportOlcrtc(ok: Boolean, cold: Boolean, log: String) = withContext(Dispatchers.IO) {
        // A working attempt (switch/OK) has a live tunnel, so flush any cold-start FAIL reports that
        // couldn't reach /report earlier (their own traffic died in the not-yet-up tunnel).
        runCatching { flushPendingOlcrtc() }
        runCatching {
            val stack = when {
                log.length <= 8000 -> log
                // Keep BOTH the START (serverHello / relay-pin / DNS / the FIRST divergence = the
                // real cause) and the END (the failure line) — not just the tail.
                else -> log.substring(0, 3500) + "\n… …\n" + log.substring(log.length - 4300)
            }
            val payload = JSONObject().apply {
                put("kind", "olcrtc")
                put("v", BuildConfig.VERSION_NAME)
                put("vc", BuildConfig.VERSION_CODE)
                put("device", Build.MANUFACTURER + " " + Build.MODEL)
                put("api", Build.VERSION.SDK_INT)
                put("id", runCatching { MaestroSub.deviceId(Application.application) }.getOrDefault(""))
                put("ts", System.currentTimeMillis())
                put("msg", (if (cold) "cold-start " else "switch ") + (if (ok) "OK" else "FAIL"))
                put("stack", stack)
            }
            // A cold-start FAIL usually CAN'T reach /report (its POST rides the dead olcrtc outbound),
            // so if the send fails, persist it and let the next working attempt flush it out.
            if (!postReport(payload)) persistPendingOlcrtc(payload)
        }
        Unit
    }

    private fun pendingOlcrtcDir(): File =
        File(Application.application.filesDir, "olcrtc_pending").apply { mkdirs() }

    private fun persistPendingOlcrtc(payload: JSONObject) {
        runCatching {
            val dir = pendingOlcrtcDir()
            dir.listFiles()?.sortedBy { it.lastModified() }?.dropLast(9)?.forEach { it.delete() }
            File(dir, "${payload.optLong("ts")}.json").writeText(payload.toString())
        }
    }

    /** Re-send olcRTC FAIL reports that couldn't reach /report while their tunnel was down. */
    fun flushPendingOlcrtc() {
        pendingOlcrtcDir().listFiles()?.sortedBy { it.lastModified() }?.forEach { f ->
            runCatching { if (postReport(JSONObject(f.readText()))) f.delete() }
        }
    }

    private fun postReport(payload: JSONObject): Boolean = runCatching {
        val url = URL(BuildConfig.BACKEND_URL.trimEnd('/') + "/report")
        val conn = (url.openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 8000
            readTimeout = 8000
            doOutput = true
            setRequestProperty("Content-Type", "application/json")
            setRequestProperty("User-Agent", HTTPClient.userAgent)
        }
        try {
            conn.outputStream.use { it.write(payload.toString().toByteArray(Charsets.UTF_8)) }
            conn.responseCode in 200..299
        } finally {
            conn.disconnect()
        }
    }.getOrDefault(false)

    private fun archivePendingJvmCrashReport() {
        val crashFile = File(workingDir, PENDING_JVM_CRASH_FILE_NAME)
        val metadataFile = File(workingDir, PENDING_JVM_METADATA_FILE_NAME)
        val configFile = File(baseDir, CONFIG_FILE_NAME)
        if (!crashFile.exists()) return
        val content = crashFile.readText().trim()
        if (content.isEmpty()) {
            crashFile.delete()
            metadataFile.delete()
            configFile.delete()
            return
        }
        val crashDate = Date(crashFile.lastModified())
        val reportDir = nextAvailableReportDir(crashDate)
        reportDir.mkdirs()
        crashFile.copyTo(File(reportDir, JVM_LOG_FILE_NAME), overwrite = true)
        crashFile.delete()
        if (metadataFile.exists()) {
            metadataFile.copyTo(File(reportDir, METADATA_FILE_NAME), overwrite = true)
            metadataFile.delete()
        }
        if (configFile.exists()) {
            val configContent = runCatching { configFile.readText() }.getOrNull()?.trim()
            if (!configContent.isNullOrEmpty()) {
                configFile.copyTo(File(reportDir, CONFIG_FILE_NAME), overwrite = true)
            }
            configFile.delete()
        }
    }

    private fun scanCrashReports(): List<CrashReport> {
        val crashReportsDir = File(workingDir, CRASH_REPORTS_DIR_NAME)
        if (!crashReportsDir.isDirectory) return emptyList()
        val directories = crashReportsDir.listFiles { file -> file.isDirectory } ?: return emptyList()
        return directories.mapNotNull { dir ->
            val date = parseTimestamp(dir.name) ?: return@mapNotNull null
            CrashReport(
                id = dir.name,
                date = date,
                directory = dir,
                isRead = File(dir, READ_MARKER_FILE_NAME).exists(),
            )
        }.sortedByDescending { it.date }
    }

    fun availableFiles(report: CrashReport): List<CrashReportFile> {
        val files = mutableListOf<CrashReportFile>()
        val metadataFile = File(report.directory, METADATA_FILE_NAME)
        if (metadataFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.METADATA, "Metadata", metadataFile))
        }
        val goLogFile = File(report.directory, GO_LOG_FILE_NAME)
        if (goLogFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.GO_LOG, "Go Crash Log", goLogFile))
        }
        val jvmLogFile = File(report.directory, JVM_LOG_FILE_NAME)
        if (jvmLogFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.JVM_LOG, "JVM Crash Log", jvmLogFile))
        }
        val configFile = File(report.directory, CONFIG_FILE_NAME)
        if (configFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.CONFIG, "Configuration", configFile))
        }
        return files
    }

    fun loadFileContent(file: CrashReportFile): String {
        if (!file.file.exists()) return ""
        val content = file.file.readText()
        if (file.kind == CrashReportFile.Kind.METADATA) {
            return runCatching {
                JSONObject(content).toString(2)
            }.getOrDefault(content)
        }
        return content
    }

    fun markAsRead(report: CrashReport) {
        File(report.directory, READ_MARKER_FILE_NAME).createNewFile()
        val updated = _reports.value.map {
            if (it.id == report.id) it.copy(isRead = true) else it
        }
        _reports.value = updated
        _unreadCount.value = updated.count { !it.isRead }
    }

    suspend fun delete(report: CrashReport) = withContext(Dispatchers.IO) {
        report.directory.deleteRecursively()
        val updated = _reports.value.filter { it.id != report.id }
        _reports.value = updated
        _unreadCount.value = updated.count { !it.isRead }
    }

    suspend fun deleteAll() = withContext(Dispatchers.IO) {
        File(workingDir, CRASH_REPORTS_DIR_NAME).deleteRecursively()
        _reports.value = emptyList()
        _unreadCount.value = 0
    }

    fun hasConfigFile(report: CrashReport): Boolean = File(report.directory, CONFIG_FILE_NAME).exists()

    suspend fun createZipArchive(report: CrashReport, includeConfig: Boolean): File = withContext(Dispatchers.IO) {
        val cacheDir = File(Application.application.cacheDir, CRASH_REPORTS_DIR_NAME)
        cacheDir.mkdirs()
        val zipFile = File(cacheDir, "${report.id}.zip")
        zipFile.delete()
        val strippedDir = File(cacheDir, report.id)
        strippedDir.deleteRecursively()
        report.directory.copyRecursively(strippedDir, overwrite = true)
        File(strippedDir, READ_MARKER_FILE_NAME).delete()
        if (!includeConfig) {
            File(strippedDir, CONFIG_FILE_NAME).delete()
        }
        Libbox.createZipArchive(strippedDir.path, zipFile.path)
        zipFile
    }

    private fun nextAvailableReportDir(date: Date): File {
        val crashReportsDir = File(workingDir, CRASH_REPORTS_DIR_NAME)
        val baseName = timestampFormat.format(date)
        var index = 0
        while (true) {
            val suffix = if (index == 0) "" else "-$index"
            val dir = File(crashReportsDir, baseName + suffix)
            if (!dir.exists()) return dir
            index++
        }
    }

    private fun parseTimestamp(name: String): Date? {
        val components = name.split("-")
        val baseName = if (components.size > 5 && components.last().toIntOrNull() != null) {
            components.dropLast(1).joinToString("-")
        } else {
            name
        }
        return try {
            timestampFormat.parse(baseName)
        } catch (_: ParseException) {
            null
        }
    }

    private fun formatTimestampISO8601(date: Date): String {
        val format = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssZ", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("UTC")
        }
        return format.format(date)
    }
}

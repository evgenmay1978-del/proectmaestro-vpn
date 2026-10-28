package com.maestrovpn.tv.bg

import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * Append-only technical diary of the tunnel start path, written where a plain `adb shell` can read it
 * (/sdcard/Android/data/<pkg>/files/diag/startup.log, or the private files dir when no external dir).
 *
 * Why it exists: the 1.0.178 startup verdict could not be checked after the fact. The engine log is
 * exposed only through the in-app log screen, and that screen attaches only while the status is
 * Started — so a failed start left the owner with a dialog and zero evidence. The engine's own
 * messages are not routed here (PlatformInterfaceWrapper has no writeLog override); these are the
 * decisions OUR code takes: config size, probe outcomes with timings, redials, fallbacks, network
 * changes, status transitions.
 *
 * Never write credentials, UUIDs, hosts or the config itself — stage, decision and numbers only.
 */
internal object StartupDiag {
    private const val MAX_BYTES = 256L * 1024
    private val lock = Any()
    private val stamp = SimpleDateFormat("dd.MM HH:mm:ss.SSS", Locale.US)

    @Volatile
    private var dir: File? = null

    /** [base] is the app's external files dir when available, the private files dir otherwise. */
    fun init(base: File?) {
        synchronized(lock) { dir = base?.let { File(it, "diag") } }
    }

    fun log(stage: String, message: String) {
        val line = stamp.format(Date()) + " " + stage + " " + message + "\n"
        synchronized(lock) {
            val base = dir ?: return
            runCatching {
                if (base.isDirectory || base.mkdirs()) {
                    val file = File(base, "startup.log")
                    if (file.length() > MAX_BYTES) {
                        val previous = File(base, "startup.log.1")
                        previous.delete()
                        file.renameTo(previous)
                    }
                    file.appendText(line)
                }
            }
        }
    }

    /** The current diary file, or null when no directory has been set. Used by tests. */
    fun file(): File? = synchronized(lock) { dir?.let { File(it, "startup.log") } }
}

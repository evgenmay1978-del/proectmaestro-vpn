package com.maestrovpn.tv.bg

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.Build
import android.os.PowerManager
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.utils.CommandClient
import io.nekohasekai.libbox.ConnectionEvents
import io.nekohasekai.libbox.LogEntry
import io.nekohasekai.libbox.OutboundGroup
import io.nekohasekai.libbox.StatusMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.io.File
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URL
import java.security.MessageDigest
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * Runtime diary of the tunnel itself, written where a plain `adb shell` can read it
 * (/sdcard/Android/data/<pkg>/files/diag/). StartupDiag records only our own decisions before
 * the engine starts; once the VPN is up, the engine log, the live selector, connection events
 * and the device network were invisible after a failed session (release builds strip Log.d and
 * libbox v1.14.4 has no writeLog hook). This object closes that hole:
 *
 *  - engine.log: engine messages, status, groups, connection events, probes, config dumps
 *  - summary.log: the subset that decides a verdict, kept under 60 KB so one adb-shell read fits
 *
 * Diagnostics must never break the tunnel: every write, probe and callback is wrapped in
 * runCatching and every dump is passed through [redact] first.
 */
internal object EngineDiag {
    private const val MAX_BYTES = 1024L * 1024
    // Kept under the 64 KB ceiling of a plain adb-shell read, so summary.log is fetchable in one shot.
    private const val SUMMARY_BYTES = 60000L

    private val lock = Any()
    private val stamp = SimpleDateFormat("dd.MM HH:mm:ss.SSS", Locale.US)
    private val queue = ArrayBlockingQueue<String>(20000)
    private val keyQueue = ArrayBlockingQueue<String>(4000)
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    @Volatile private var dir: File? = null
    @Volatile private var attached = false
    @Volatile private var dropped = 0L
    @Volatile private var lastStatusLog = 0L
    @Volatile private var lastStatus = "status none"
    @Volatile private var lastGroups = "groups none"
    @Volatile private var lastDownlinkTotal = 0L
    @Volatile private var lastUplinkTotal = 0L
    @Volatile private var lastProbeAt = 0L

    private var client: CommandClient? = null
    private var sampler: Job? = null

    private val secretKey: Regex? = runCatching {
        Regex(
            "(\"(?:uuid|password|password1|public_key|short_id|token|secret|auth_str|auth-str|private_key|" +
                "pre_shared_key|obfs_password|obfs-password|psk|key|cookie|alter_id_salt)\"\\s*:\\s*)\"[^\"]*\"",
            RegexOption.IGNORE_CASE,
        )
    }.getOrNull()

    /** [base] is the app's external files dir when available, the private files dir otherwise. */
    fun init(base: File?) {
        synchronized(lock) {
            val target = base?.let { File(it, "diag") }
            if (target == null || target == dir) return
            dir = target
            runCatching { target.mkdirs() }
            startWriterLocked()
        }
    }

    fun isInitialized(): Boolean = dir != null

    fun log(tag: String, message: String) {
        if (dir == null) return
        val at = synchronized(stamp) { stamp.format(Date()) }
        val line = at + " [" + tag + "] " + message.replace('\n', ' ').take(4000)
        if (!queue.offer(line)) dropped++
        if (isKey(tag, message) && !keyQueue.offer(line)) dropped++
    }

    /** summary.log keeps transitions, errors, probes and selector changes; engine.log keeps all. */
    private fun isKey(tag: String, message: String): Boolean = when (tag) {
        "eng" -> message.startsWith("lvl=0") || message.startsWith("lvl=1") ||
            message.contains("error", true) || message.contains("failed", true) ||
            message.contains("timeout", true) || message.contains("refused", true) ||
            message.contains("reject", true) || message.contains("dns", true)

        "stat", "hb" -> false
        else -> true
    }

    /** One line per config stage: bytes + digest, so a silent rewrite of the bytes we hand over is visible. */
    fun stage(name: String, config: String) {
        log(
            "cfg",
            "stage=" + name + " bytes=" + config.length + " sha8=" + sha8(config) +
                " types=" + occurrences(config, "\"type\":") + " ruleSets=" + occurrences(config, "\"rule_set\"") +
                " sniff=" + config.contains("\"sniff\"", true) + " ipv6=" + config.contains("\"::\"") +
                " final=" + finalOf(config),
        )
    }

    fun dump(name: String, content: String) {
        val base = dir ?: return
        runCatching {
            if (!base.isDirectory && !base.mkdirs()) return
            File(base, name + ".log").writeText(content.take(2 * 1024 * 1024))
            log("dump", "wrote " + name + ".log bytes=" + content.length)
        }.onFailure { log("dump", "failed " + name + ": " + it.javaClass.simpleName) }
    }

    fun redact(text: String): String {
        val pattern = secretKey ?: return text
        return runCatching { text.replace(pattern) { it.groupValues[1] + "\"***\"" } }.getOrDefault(text)
    }

    private fun sha8(text: String): String = runCatching {
        MessageDigest.getInstance("SHA-256").digest(text.toByteArray()).take(4)
            .joinToString("") { String.format(Locale.US, "%02x", it) }
    }.getOrDefault("?")

    private fun occurrences(text: String, needle: String): Int = text.split(needle).size - 1

    private fun finalOf(config: String): String {
        val index = config.lastIndexOf("\"final\"")
        if (index < 0) return "-"
        return config.substring(index, minOf(config.length, index + 48))
    }

    private fun startWriterLocked() {
        thread(name = "engine-diag", isDaemon = true) { writeLoop() }
    }

    private fun writeLoop() {
        while (true) {
            val first = runCatching { queue.poll(2, TimeUnit.SECONDS) }.getOrNull() ?: continue
            val plain = StringBuilder().append(first).append('\n')
            var extra = queue.poll()
            var guard = 0
            while (extra != null && guard < 500) {
                plain.append(extra).append('\n')
                guard++
                extra = queue.poll()
            }
            write("engine.log", plain.toString(), MAX_BYTES)

            val keyFirst = keyQueue.poll() ?: continue
            val summary = StringBuilder().append(keyFirst).append('\n')
            var more = keyQueue.poll()
            var guard2 = 0
            while (more != null && guard2 < 500) {
                summary.append(more).append('\n')
                guard2++
                more = keyQueue.poll()
            }
            write("summary.log", summary.toString(), SUMMARY_BYTES)
        }
    }

    private fun write(name: String, text: String, max: Long) {
        val base = dir ?: return
        synchronized(lock) {
            runCatching {
                if (!base.isDirectory && !base.mkdirs()) return
                val file = File(base, name)
                if (file.length() > max) {
                    val previous = File(base, name + ".1")
                    previous.delete()
                    file.renameTo(previous)
                }
                file.appendText(text)
            }
        }
    }

    /** Rolling copy in Download-visible storage, so the diary can be pulled even if the app dies. */
    fun publish(context: Context?, suffix: String) {
        val base = dir ?: return
        val external = context?.getExternalFilesDir(null) ?: return
        runCatching {
            val folder = File(external, "diag")
            if (!folder.isDirectory && !folder.mkdirs()) return
            for (name in arrayOf("engine.log", "summary.log")) {
                val source = File(base, name)
                if (!source.isFile) continue
                source.copyTo(File(folder, name.replace(".log", "-" + suffix + ".log")), overwrite = true)
            }
        }.onFailure { log("dump", "publish failed: " + it.javaClass.simpleName) }
    }

    /**
     * Attaches to the running engine: live status, selector groups, engine messages and
     * connection events, plus a 15 s heartbeat and periodic reachability probes.
     */
    fun attach() {
        if (attached) return
        attached = true
        log(
            "diag",
            "attach build=" + BuildConfig.VERSION_NAME + "/" + BuildConfig.VERSION_CODE +
                " sdk=" + Build.VERSION.SDK_INT + " abi=" + Build.SUPPORTED_ABIS.firstOrNull() +
                " device=" + Build.MANUFACTURER + " " + Build.MODEL,
        )
        networkSnapshot("attach")
        settingsSnapshot()
        runCatching {
            val diagnostics = CommandClient(
                scope,
                listOf(
                    CommandClient.ConnectionType.Status,
                    CommandClient.ConnectionType.Groups,
                    CommandClient.ConnectionType.Log,
                    CommandClient.ConnectionType.Connections,
                ),
                Bridge,
            )
            client = diagnostics
            diagnostics.connect()
        }.onFailure { log("diag", "command client failed: " + it.javaClass.simpleName + ": " + it.message) }
        probes("attach")
        sampler = scope.launch { sampleLoop() }
    }

    fun detach(reason: String) {
        if (!attached) return
        attached = false
        runCatching { sampler?.cancel() }
        sampler = null
        runCatching { client?.disconnect() }
        client = null
        log("diag", "detach: " + reason)
    }

    private suspend fun sampleLoop() {
        var tick = 0
        while (attached) {
            delay(15000)
            tick++
            runCatching {
                log("hb", lastStatus + " | " + lastGroups + " | total down=" + lastDownlinkTotal + " up=" + lastUplinkTotal)
            }.onFailure { log("hb", "sample failed: " + it.javaClass.simpleName) }
            if (tick % 3 == 0) probes("tick")
        }
    }

    internal fun onStatus(status: StatusMessage) {
        val now = System.currentTimeMillis()
        runCatching {
            lastDownlinkTotal = status.downlinkTotal
            lastUplinkTotal = status.uplinkTotal
            lastStatus = "conns=" + status.connectionsIn + "/" + status.connectionsOut +
                " down=" + status.downlink + "B/s up=" + status.uplink + "B/s" +
                " gor=" + status.goroutines + " mem=" + status.memory +
                " traffic=" + status.trafficAvailable
            if (now - lastStatusLog > 5000) {
                lastStatusLog = now
                log("stat", lastStatus)
            }
        }.onFailure { log("stat", "unreadable status: " + it.javaClass.simpleName) }
    }

    internal fun onGroups(groups: MutableList<OutboundGroup>) {
        runCatching {
            val text = StringBuilder()
            for (group in groups) {
                text.append(group.tag).append('(').append(group.type).append(")->").append(group.selected).append('[')
                val items = group.items
                var count = 0
                while (items.hasNext() && count < 24) {
                    val item = items.next()
                    text.append(item.tag).append(':').append(item.urlTestDelay).append(' ')
                    count++
                }
                text.append("] ")
            }
            val rendered = text.toString().trim()
            if (rendered.isNotEmpty() && rendered != lastGroups) {
                lastGroups = rendered
                log("grp", rendered)
            }
        }.onFailure { log("grp", "unreadable groups: " + it.javaClass.simpleName) }
    }

    internal fun onEvents(events: ConnectionEvents) {
        runCatching {
            val iterator = events.iterator()
            var count = 0
            while (iterator.hasNext() && count < 40) {
                val event = iterator.next()
                val connection = event.connection
                log(
                    "cev",
                    "type=" + event.type + " id=" + event.getID() +
                        " out=" + connection.outbound + "/" + connection.outboundType +
                        " in=" + connection.inbound + " net=" + connection.network +
                        " proto=" + connection.protocol + " dest=" + connection.destination +
                        " rule=" + connection.rule + " up=" + event.uplinkDelta + " down=" + event.downlinkDelta,
                )
                count++
            }
        }.onFailure { log("cev", "unreadable events: " + it.javaClass.simpleName) }
    }

    private object Bridge : CommandClient.Handler {
        override fun onConnected() = EngineDiag.log("cmd", "connected")

        override fun onDisconnected() = EngineDiag.log("cmd", "disconnected")

        override fun onConnectionError(kind: CommandClient.ConnectionErrorKind, message: String) =
            EngineDiag.log("cmd", "error kind=" + kind + " " + message)

        override fun setDefaultLogLevel(level: Int) = EngineDiag.log("cmd", "defaultLogLevel=" + level)

        override fun appendLogs(message: List<LogEntry>) {
            for (entry in message) EngineDiag.log("eng", "lvl=" + entry.level + " " + entry.message)
        }

        override fun updateStatus(status: StatusMessage) = EngineDiag.onStatus(status)

        override fun updateGroups(newGroups: MutableList<OutboundGroup>) = EngineDiag.onGroups(newGroups)

        override fun writeConnectionEvents(events: ConnectionEvents) = EngineDiag.onEvents(events)
    }

    /** Routing knobs that decide which apps ride the tunnel at all — a silent "nothing works" source. */
    fun settingsSnapshot() {
        runCatching {
            val settings = com.maestrovpn.tv.database.Settings
            log(
                "cfg",
                "settings perApp=" + settings.perAppProxyEnabled +
                    " mode=" + settings.getEffectivePerAppProxyMode() +
                    " list=" + settings.getEffectivePerAppProxyList() +
                    " allowBypass=" + settings.allowBypass +
                    " systemProxy=" + settings.systemProxyEnabled +
                    " autoRedirect=" + settings.autoRedirect +
                    " startedByUser=" + settings.startedByUser +
                    " serviceMode=" + settings.serviceMode,
            )
        }.onFailure { log("cfg", "settings snapshot failed: " + it.javaClass.simpleName) }
    }

    /** What the OS thinks the phone is connected to — the half of the tunnel picture we control. */
    @Suppress("NewApi") // LinkProperties#getMtu появился в API 29; вызов под проверкой SDK_INT ниже
    fun networkSnapshot(reason: String) {
        runCatching {
            val context = Application.application
            val manager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager
            if (manager == null) {
                log("net", reason + ": no ConnectivityManager")
                return@runCatching
            }
            val network = manager.activeNetwork
            if (network == null) {
                log("net", reason + ": no active network")
                return@runCatching
            }
            val capabilities = manager.getNetworkCapabilities(network)
            val properties = manager.getLinkProperties(network)
            val transports = StringBuilder()
            if (capabilities != null) {
                if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) transports.append("cell ")
                if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) transports.append("wifi ")
                if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) transports.append("vpn ")
                if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) transports.append("eth ")
            }
            val linkMtu: Int? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) properties?.mtu else null
            log(
                "net",
                reason + ": transports=" + transports.toString().trim() +
                    " iface=" + properties?.interfaceName + " mtu=" + linkMtu +
                    " dns=" + properties?.dnsServers?.joinToString(",") +
                    " addrs=" + properties?.linkAddresses?.joinToString(",") +
                    " down=" + capabilities?.linkDownstreamBandwidthKbps + "kbps" +
                    " validated=" + capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED),
            )
        }.onFailure { log("net", reason + ": snapshot failed: " + it.javaClass.simpleName) }

        runCatching {
            val context = Application.application
            val power = context.getSystemService(Context.POWER_SERVICE) as? PowerManager
            val battery = context.getSystemService(Context.BATTERY_SERVICE) as? BatteryManager
            log(
                "net",
                "power saver=" + power?.isPowerSaveMode +
                    " ignoringOptimizations=" + power?.isIgnoringBatteryOptimizations(context.packageName) +
                    " battery=" + (battery?.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) ?: -1) + "%",
            )
        }.onFailure { log("net", "power snapshot failed: " + it.javaClass.simpleName) }
    }

    /** DNS/TCP/HTTP probes: they answer "is the tunnel passing traffic at all" by themselves. */
    fun probes(reason: String) {
        val now = System.currentTimeMillis()
        if (now - lastProbeAt < 5000) return
        lastProbeAt = now
        scope.launch {
            runCatching {
                val started = System.currentTimeMillis()
                probeDns(reason, "youtube.com")
                probeDns(reason, "api.telegram.org")
                probeTcp(reason, "1.1.1.1", 443)
                probeTcp(reason, "8.8.8.8", 53)
                probeHttps(reason, "https://www.gstatic.com/generate_204")
                probeHttps(reason, "https://api.telegram.org")
                log("probe", reason + " finished in " + (System.currentTimeMillis() - started) + "ms")
            }.onFailure { log("probe", reason + " failed: " + it.javaClass.simpleName) }
        }
    }

    private fun probeDns(reason: String, host: String) {
        val started = System.currentTimeMillis()
        runCatching {
            val addresses = InetAddress.getAllByName(host)
            log(
                "probe",
                reason + " dns " + host + " -> " + addresses.take(3).joinToString(",") { it.hostAddress ?: "?" } +
                    " in " + (System.currentTimeMillis() - started) + "ms",
            )
        }.onFailure {
            log("probe", reason + " dns " + host + " FAIL " + it.javaClass.simpleName + ": " + it.message +
                " in " + (System.currentTimeMillis() - started) + "ms")
        }
    }

    private fun probeTcp(reason: String, host: String, port: Int) {
        val started = System.currentTimeMillis()
        runCatching {
            Socket().use { socket ->
                socket.connect(InetSocketAddress(host, port), 3000)
                log("probe", reason + " tcp " + host + ":" + port + " OK in " + (System.currentTimeMillis() - started) + "ms")
            }
        }.onFailure {
            log("probe", reason + " tcp " + host + ":" + port + " FAIL " + it.javaClass.simpleName + ": " + it.message +
                " in " + (System.currentTimeMillis() - started) + "ms")
        }
    }

    private fun probeHttps(reason: String, url: String) {
        val started = System.currentTimeMillis()
        runCatching {
            val connection = URL(url).openConnection() as HttpURLConnection
            connection.connectTimeout = 5000
            connection.readTimeout = 5000
            connection.requestMethod = "HEAD"
            connection.setRequestProperty("Connection", "close")
            try {
                log("probe", reason + " https " + url + " -> " + connection.responseCode +
                    " in " + (System.currentTimeMillis() - started) + "ms")
            } finally {
                runCatching { connection.disconnect() }
            }
        }.onFailure {
            log("probe", reason + " https " + url + " FAIL " + it.javaClass.simpleName + ": " + it.message +
                " in " + (System.currentTimeMillis() - started) + "ms")
        }
    }

    /** Records who asked for a stop, so "the VPN turned itself off" can be answered with a stack. */
    fun logStackTrace(tag: String, reason: String) {
        val trace = Throwable().stackTrace.take(8).joinToString(" <- ") { frame ->
            frame.methodName + "(" + frame.fileName + ":" + frame.lineNumber + ")"
        }
        log(tag, reason + " caller=" + trace)
    }

    /** The app's own permission bundle silently decides whether the tun can come up. */
    fun permissionSnapshot(context: Context?) {
        if (context == null) return
        runCatching {
            val granted = listOf(
                Manifest.permission.ACCESS_NETWORK_STATE,
                Manifest.permission.POST_NOTIFICATIONS,
                Manifest.permission.FOREGROUND_SERVICE,
                Manifest.permission.RECEIVE_BOOT_COMPLETED,
            ).joinToString(",") { name ->
                name.substringAfterLast('.') + "=" + (context.checkSelfPermission(name) == PackageManager.PERMISSION_GRANTED)
            }
            log("diag", "permissions " + granted)
        }.onFailure { log("diag", "permissions failed: " + it.javaClass.simpleName) }
    }
}

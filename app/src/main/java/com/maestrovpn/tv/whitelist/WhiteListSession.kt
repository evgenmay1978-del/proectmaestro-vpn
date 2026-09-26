package com.maestrovpn.tv.whitelist

import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.SystemClock
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.DefaultNetworkListener
import com.maestrovpn.tv.bg.DefaultNetworkMonitor
import com.maestrovpn.tv.bg.VPNService
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.DeviceFormFactor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import java.net.Inet4Address
import java.net.InetAddress
import java.net.ServerSocket
import java.security.SecureRandom
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/** Only BoxService owns this capability; the foreground preview cannot renew a session. */
internal class WhiteListSession(private val vpn: VPNService, private val onExpired: (WhiteListSelection.Request, String?) -> Unit) {
    companion object {
        private val ids = AtomicInteger()
        private val expiryExecutor = Executors.newSingleThreadScheduledExecutor { Thread(it, "cdn-lease-expiry").apply { isDaemon = true } }
        private val stopExecutor = Executors.newCachedThreadPool { Thread(it, "cdn-native-stop").apply { isDaemon = true } }
        private val dnsExecutor = Executors.newFixedThreadPool(2) { Thread(it, "cdn-network-dns").apply { isDaemon = true } }
        fun isCellular(network: Network?): Boolean = try {
            val caps = network?.let { Application.connectivity.getNetworkCapabilities(it) }
            caps != null && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) &&
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) &&
                !caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) &&
                !caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) &&
                !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) &&
                Application.connectivity.allNetworks.none { candidate ->
                    val other = Application.connectivity.getNetworkCapabilities(candidate)
                    other != null && other.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) &&
                        other.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
                        other.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                }
        } catch (_: Exception) { false }

        fun network(): Network? {
            return try {
                val active = Application.connectivity.activeNetwork
                val activeCaps = active?.let { Application.connectivity.getNetworkCapabilities(it) }
                val candidate = if (activeCaps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true) active
                    else DefaultNetworkMonitor.defaultNetwork
                if (candidate == null) return null
                candidate.takeIf { isCellular(it) }
            } catch (_: Exception) { null }
        }
    }

    private class Permit(val id: Long, val request: WhiteListSelection.Request, val network: Network,
        val route: WhiteListRuntimeRoute, val desiredGeneration: Long, @Volatile var deadline: Long,
        val ordinaryTag: String?) {
        val wifiGuard = Any()
        var wifiCallback: ConnectivityManager.NetworkCallback? = null
    }
    /** Startup allowance for the child process; the steady state keeps the strict deadline. */
    private val firstRenewalGraceMs = 8_000L
    private val permit = AtomicReference<Permit?>()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var renewal: Job? = null
    private val invalidated: () -> Unit = { permit.get()?.let { expire(it) } }
    private val listenerKey = AtomicReference<Any?>()
    @Volatile private var stopPending: java.util.concurrent.Future<*>? = null

    suspend fun prepare(base: String, request: WhiteListSelection.Request): String? {
        close()
        // Native is a single engine. A replacement cannot race the old engine's stop.
        stopPending?.get(2, TimeUnit.SECONDS)
        if (DeviceFormFactor.isTelevision(vpn) || !request.tag.startsWith("cdn:") ||
            !WhiteListSelection.matches(request) || SystemClock.elapsedRealtime() - request.requestedAt !in 0..60_000 || !XhttpProcess.available()) return null
        val network = network() ?: return null
        val subscription = ProfileManager.get(request.profileId)?.typed?.remoteURL ?: return null
        val initialFetchStarted = SystemClock.elapsedRealtime()
        val runtime = WhiteListRuntimeClient.fetch(subscription, network) ?: return null
        var lastFetchMs = (SystemClock.elapsedRealtime() - initialFetchStarted).coerceAtLeast(0L)
        val route = runtime.profiles.singleOrNull { it.tag == request.tag } ?: return null
        val lookup = dnsExecutor.submit<Array<InetAddress>> { network.getAllByName(route.address) }
        val addresses = try { lookup.get(1, TimeUnit.SECONDS) } finally { lookup.cancel(true) }
        val address = addresses.filterIsInstance<Inet4Address>().firstOrNull {
            !it.isAnyLocalAddress && !it.isLoopbackAddress && !it.isLinkLocalAddress && !it.isSiteLocalAddress && !it.isMulticastAddress
        }?.hostAddress ?: return null
        if (!runtime.fresh(SystemClock.elapsedRealtime()) || !WhiteListSelection.matches(request) || network != WhiteListSession.network()) return null
        val id = ids.incrementAndGet().takeIf { it > 0 }?.toLong() ?: return null
        val port = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
        val random = SecureRandom()
        fun credential(): String = android.util.Base64.encodeToString(ByteArray(24).also(random::nextBytes),
            android.util.Base64.URL_SAFE or android.util.Base64.NO_PADDING or android.util.Base64.NO_WRAP)
        val user = credential()
        val pass = credential()
        val content = WhiteListConfig.inject(base, route, address, port, user, pass)
        val live = Permit(id, request, network, route, runtime.desiredGeneration, runtime.deadlineMillis,
            WhiteListConfig.ordinaryTag(base))
        permit.set(live)
        WhiteListSelection.addInvalidation(invalidated)
        listenerKey.set(live)
        DefaultNetworkListener.start(live) {
            if (it != network || !isCellular(it)) expire(live, restoreOrdinary = true)
        }
        if (!watchWifi(live) || !valid(live)) {
            DefaultNetworkListener.stop(live)
            expire(live, restoreOrdinary = WhiteListSession.network() != live.network)
            return null
        }
        // The native client is a separate process now: a mobile round trip to the panel (~2 s) plus
        // the child's startup (~1.5-3 s) can outlast the few-second authorization lease, so the
        // first deadline is stretched to cover the startup. The first successful renewal replaces
        // it with the server's strict deadline, and a lease that is never renewed still ends the
        // session here — valid(live) stays the single decision point.
        val startupDeadline = SystemClock.elapsedRealtime() + firstRenewalGraceMs
        if (live.deadline < startupDeadline) live.deadline = startupDeadline
        armExpiry(live)
        // The native client is a separate process now: bringing it up takes a second or two, while
        // the authorization lease stays only a few seconds long (fresh_until_unix - issued_at_unix is
        // bounded to 1..5 s on both sides). The renewal loop therefore has to run BEFORE the child
        // starts, otherwise armExpiry tears the session down while the child is still coming up —
        // measured on the owner phone 26.09.2026: prepare() reached watchWifi, expire() fired 1.4 s
        // later, and the child was killed before it could serve anything.
        renewal = scope.launch {
            while (valid(live)) {
                val remaining = live.deadline - SystemClock.elapsedRealtime()
                val waitMs = remaining - lastFetchMs - 250L
                if (waitMs >= 100L) delay(waitMs.coerceAtMost(1_000L))
                if (!valid(live)) {
                    expire(live, restoreOrdinary = WhiteListSession.network() != live.network)
                    break
                }
                val fetchStarted = SystemClock.elapsedRealtime()
                val fresh = WhiteListRuntimeClient.fetch(subscription, network)
                lastFetchMs = (SystemClock.elapsedRealtime() - fetchStarted).coerceAtLeast(0L)
                synchronized(live) {
                    // A dropped or late refresh must not tear down a lease that is still valid: the
                    // loop retries while valid(live) keeps the session alive, and the armed expiry
                    // still ends it when the lease actually lapses. Only a *different* authorization
                    // — another generation or another route — means this session has to stop now.
                    // Measured on the owner phone 26.09.2026: a single 3 s refresh timeout killed an
                    // otherwise healthy CDN session 30 s in.
                    val foreign = fresh != null && (fresh.desiredGeneration != live.desiredGeneration ||
                        fresh.profiles.singleOrNull { it.tag == request.tag } != live.route)
                    when {
                        foreign -> expire(live, restoreOrdinary = WhiteListSession.network() != live.network)
                        fresh == null || !fresh.fresh(SystemClock.elapsedRealtime()) || !valid(live) -> Unit
                        else -> {
                            live.deadline = fresh.deadlineMillis
                            armExpiry(live)
                        }
                    }
                }
            }
        }
        val payload = WhiteListConfig.payload(route, address, port, user, pass)
        val result = try { XhttpProcess.start(id, payload) } finally { payload.fill(0) }
        if (result != 0 || !valid(live)) {
            expire(live, restoreOrdinary = WhiteListSession.network() != live.network)
            stopPending = stopExecutor.submit { XhttpProcess.stop(id) }
            return null
        }
        return content
    }

    fun ready(request: WhiteListSelection.Request): Boolean = permit.get()?.let { it.request == request && valid(it) } == true
    private fun valid(live: Permit): Boolean = permit.get() === live && SystemClock.elapsedRealtime() < live.deadline &&
        WhiteListSelection.matches(live.request) && network() == live.network

    private fun watchWifi(live: Permit): Boolean = synchronized(live.wifiGuard) {
        if (permit.get() !== live) return@synchronized false
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                // Also revoke when Wi-Fi is connected but Android temporarily keeps
                // cellular as its preferred network. No traffic probes are involved.
                expire(live, restoreOrdinary = true)
            }
        }
        live.wifiCallback = callback
        try {
            Application.connectivity.registerNetworkCallback(
                NetworkRequest.Builder()
                    .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    .build(),
                callback,
            )
            permit.get() === live
        } catch (_: Exception) {
            live.wifiCallback = null
            false
        }
    }

    private fun stopWatchingWifi(live: Permit) {
        val callback = synchronized(live.wifiGuard) { live.wifiCallback.also { live.wifiCallback = null } }
        if (callback != null) runCatching { Application.connectivity.unregisterNetworkCallback(callback) }
    }

    private fun armExpiry(live: Permit) {
        expiryExecutor.schedule({
            synchronized(live) {
                if (permit.get() === live && !valid(live)) expire(live, restoreOrdinary = network() != live.network)
            }
        }, (live.deadline - SystemClock.elapsedRealtime()).coerceAtLeast(0), TimeUnit.MILLISECONDS)
    }
    private fun expire(live: Permit, restoreOrdinary: Boolean = false) {
        if (!permit.compareAndSet(live, null)) return
        renewal?.cancel()
        WhiteListSelection.removeInvalidation(invalidated)
        stopPending = stopExecutor.submit { XhttpProcess.stop(live.id) }
        stopWatchingWifi(live)
        listenerKey.compareAndSet(live, null)
        stopExecutor.execute { runBlocking { DefaultNetworkListener.stop(live) } }
        onExpired(live.request, live.ordinaryTag.takeIf { restoreOrdinary })
    }
    fun close() {
        val live = permit.getAndSet(null)
        renewal?.cancel()
        renewal = null
        if (live != null) {
            stopPending = stopExecutor.submit { XhttpProcess.stop(live.id) }
            stopWatchingWifi(live)
        }
        WhiteListSelection.removeInvalidation(invalidated)
        listenerKey.getAndSet(null)?.let { key -> stopExecutor.execute { runBlocking { DefaultNetworkListener.stop(key) } } }
    }
    fun destroy() { close(); scope.cancel() }
}

package com.maestrovpn.tv.whitelist

import android.net.Network
import android.net.NetworkCapabilities
import android.os.ParcelFileDescriptor
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
internal class WhiteListSession(private val vpn: VPNService, private val onExpired: (WhiteListSelection.Request) -> Unit) : SocketProtector {
    companion object {
        private val ids = AtomicInteger()
        private val expiryExecutor = Executors.newSingleThreadScheduledExecutor { Thread(it, "cdn-lease-expiry").apply { isDaemon = true } }
        private val stopExecutor = Executors.newCachedThreadPool { Thread(it, "cdn-native-stop").apply { isDaemon = true } }
        private val dnsExecutor = Executors.newFixedThreadPool(2) { Thread(it, "cdn-network-dns").apply { isDaemon = true } }
        fun network(): Network? {
            return try {
                val active = Application.connectivity.activeNetwork
                val activeCaps = active?.let { Application.connectivity.getNetworkCapabilities(it) }
                val candidate = if (activeCaps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true) active
                    else DefaultNetworkMonitor.defaultNetwork
                if (candidate == null) return null
                val caps = Application.connectivity.getNetworkCapabilities(candidate) ?: return null
                candidate.takeIf { caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) && !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) }
            } catch (_: Exception) { null }
        }
    }

    private class Permit(val id: Long, val request: WhiteListSelection.Request, val network: Network,
        val route: WhiteListRuntimeRoute, val desiredGeneration: Long, @Volatile var deadline: Long)
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
            !WhiteListSelection.matches(request) || SystemClock.elapsedRealtime() - request.requestedAt !in 0..60_000 || !XhttpNative.available()) return null
        val network = network() ?: return null
        val subscription = ProfileManager.get(request.profileId)?.typed?.remoteURL ?: return null
        val runtime = WhiteListRuntimeClient.fetch(subscription, network) ?: return null
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
        val content = WhiteListConfig.inject(base, route, port, user, pass)
        val live = Permit(id, request, network, route, runtime.desiredGeneration, runtime.deadlineMillis)
        permit.set(live)
        WhiteListSelection.addInvalidation(invalidated)
        listenerKey.set(live)
        DefaultNetworkListener.start(live) { if (it != network) expire(live) }
        if (!valid(live)) { DefaultNetworkListener.stop(live); expire(live); return null }
        armExpiry(live)
        val payload = WhiteListConfig.payload(route, address, port, user, pass)
        val result = try { XhttpNative.nativeStart(id, payload, this) } finally { payload.fill(0) }
        if (result != 0 || !valid(live)) {
            expire(live)
            stopPending = stopExecutor.submit { XhttpNative.nativeStop(id) }
            return null
        }
        renewal = scope.launch {
            while (valid(live)) {
                delay(((live.deadline - SystemClock.elapsedRealtime()) / 2).coerceIn(100, 1_000))
                val fresh = WhiteListRuntimeClient.fetch(subscription, network)
                synchronized(live) {
                    if (!valid(live) || fresh == null || !fresh.fresh(SystemClock.elapsedRealtime()) ||
                        fresh.desiredGeneration != live.desiredGeneration || fresh.profiles.singleOrNull { it.tag == request.tag } != live.route) {
                        expire(live)
                    } else {
                        live.deadline = fresh.deadlineMillis
                        armExpiry(live)
                    }
                }
            }
        }
        return content
    }

    fun ready(request: WhiteListSelection.Request): Boolean = permit.get()?.let { it.request == request && valid(it) } == true
    private fun valid(live: Permit): Boolean = permit.get() === live && SystemClock.elapsedRealtime() < live.deadline &&
        WhiteListSelection.matches(live.request) && network() == live.network

    override fun protectSocket(sessionId: Long, fd: Int): Boolean {
        val live = permit.get() ?: return false
        if (sessionId != live.id || fd < 0 || !valid(live)) return false
        return try {
            if (!vpn.protect(fd)) false else {
                ParcelFileDescriptor.fromFd(fd).use { live.network.bindSocket(it.fileDescriptor) }
                valid(live)
            }
        } catch (_: Exception) { false }
    }

    private fun armExpiry(live: Permit) {
        expiryExecutor.schedule({
            synchronized(live) { if (permit.get() === live && !valid(live)) expire(live) }
        }, (live.deadline - SystemClock.elapsedRealtime()).coerceAtLeast(0), TimeUnit.MILLISECONDS)
    }
    private fun expire(live: Permit) {
        if (!permit.compareAndSet(live, null)) return
        renewal?.cancel()
        WhiteListSelection.removeInvalidation(invalidated)
        stopPending = stopExecutor.submit { XhttpNative.nativeStop(live.id) }
        listenerKey.compareAndSet(live, null)
        stopExecutor.execute { runBlocking { DefaultNetworkListener.stop(live) } }
        onExpired(live.request)
    }
    fun close() {
        val live = permit.getAndSet(null)
        renewal?.cancel()
        renewal = null
        if (live != null) stopPending = stopExecutor.submit { XhttpNative.nativeStop(live.id) }
        WhiteListSelection.removeInvalidation(invalidated)
        listenerKey.getAndSet(null)?.let { key -> stopExecutor.execute { runBlocking { DefaultNetworkListener.stop(key) } } }
    }
    fun destroy() { close(); scope.cancel() }
}

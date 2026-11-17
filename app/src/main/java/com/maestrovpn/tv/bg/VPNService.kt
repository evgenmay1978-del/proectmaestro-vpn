package com.maestrovpn.tv.bg

import android.content.Intent
import android.content.pm.PackageManager.NameNotFoundException
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Build
import android.os.IBinder
import android.util.Log
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.Notification
import io.nekohasekai.libbox.TunOptions
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.ktx.toIpPrefix
import com.maestrovpn.tv.ktx.toList
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext

class VPNService :
    VpnService(),
    PlatformInterfaceWrapper {
    companion object {
        private const val TAG = "VPNService"

        /**
         * The live tunnel. SubXhttpRuntime calls protect() on its latency probes: without it a
         * probe socket leaves through the tun, the engine answers locally and the list shows
         * "1 мс" for a node that is 60+ ms away (measured on the owner's phone, 14.11.2026).
         */
        @Volatile
        internal var current: VPNService? = null
    }

    private val service = BoxService(this, this)

    override fun onCreate() {
        super.onCreate()
        current = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int) = service.onStartCommand()

    override fun onBind(intent: Intent): IBinder {
        val binder = super.onBind(intent)
        if (binder != null) {
            return binder
        }
        return service.onBind()
    }

    override fun onDestroy() {
        if (current === this) current = null
        service.onDestroy()
    }

    override fun onRevoke() {
        runBlocking {
            withContext(Dispatchers.Main) {
                service.onRevoke()
            }
        }
    }

    override fun autoDetectInterfaceControl(fd: Int) {
        protect(fd)
    }

    var systemProxyAvailable = false
    var systemProxyEnabled = false

    override fun openTun(options: TunOptions): Int {
        if (prepare(this) != null) error("android: missing vpn permission")

        // TUN MTU clamp. sing-box defaults an UNSET mtu to 9000. Keep 1280: the panel ships the
        // tun with "stack": "system", which has no MSS clamping, so a 1500 mtu silently drops
        // everything above the uplink path MTU — the live run on 2026-11-17 showed the VPN
        // "connected" with no TLS handshake passing at all (no YouTube, no Telegram, not even
        // what worked without the VPN). A config that already carries a smaller mtu is passed
        // through unchanged.
        val tunMtu = options.mtu.let { if (it in 1..1280) it else 1280 }
        EngineDiag.log(
            "tun",
            "request mtu=" + options.mtu + " autoRoute=" + options.autoRoute + " dnsMode=" + options.dnsMode +
                " httpProxy=" + options.isHTTPProxyEnabled + " allowBypass=" + Settings.allowBypass,
        )

        val includeNames = StringBuilder()
        val excludeNames = StringBuilder()

        val builder =
            Builder()
                .setSession("MaestroVPN")
                .setMtu(tunMtu)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            builder.setMetered(false)
        }

        if (Settings.allowBypass) {
            builder.allowBypass()
        }

        val inet4Address = options.inet4Address
        var inet4Count = 0
        while (inet4Address.hasNext()) {
            val address = inet4Address.next()
            builder.addAddress(address.address(), address.prefix())
            inet4Count++
        }

        val inet6Address = options.inet6Address
        var inet6Count = 0
        while (inet6Address.hasNext()) {
            val address = inet6Address.next()
            builder.addAddress(address.address(), address.prefix())
            inet6Count++
        }

        // The device-side proof that the tun captured both families; the engine log cannot be read
        // after a failed start, so the counts go to the diary as well.
        Log.d("VPNService", "tun families: inet4=" + inet4Count + " inet6=" + inet6Count + " mtu=" + tunMtu)
        StartupDiag.log("tun", "families inet4=" + inet4Count + " inet6=" + inet6Count + " mtu=" + tunMtu)
        EngineDiag.log("tun", "families inet4=" + inet4Count + " inet6=" + inet6Count + " mtu=" + tunMtu)

        if (options.autoRoute) {
            if (options.dnsMode.value != Libbox.DNSModeDisabled) {
                val dnsServerAddress = options.dnsServerAddress
                while (dnsServerAddress.hasNext()) {
                    builder.addDnsServer(dnsServerAddress.next())
                }
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                val inet4RouteAddress = options.inet4RouteAddress
                if (inet4RouteAddress.hasNext()) {
                    while (inet4RouteAddress.hasNext()) {
                        builder.addRoute(inet4RouteAddress.next().toIpPrefix())
                    }
                } else if (options.inet4Address.hasNext()) {
                    builder.addRoute("0.0.0.0", 0)
                }

                val inet6RouteAddress = options.inet6RouteAddress
                if (inet6RouteAddress.hasNext()) {
                    while (inet6RouteAddress.hasNext()) {
                        builder.addRoute(inet6RouteAddress.next().toIpPrefix())
                    }
                } else if (options.inet6Address.hasNext()) {
                    builder.addRoute("::", 0)
                }

                val inet4RouteExcludeAddress = options.inet4RouteExcludeAddress
                while (inet4RouteExcludeAddress.hasNext()) {
                    builder.excludeRoute(inet4RouteExcludeAddress.next().toIpPrefix())
                }

                val inet6RouteExcludeAddress = options.inet6RouteExcludeAddress
                while (inet6RouteExcludeAddress.hasNext()) {
                    builder.excludeRoute(inet6RouteExcludeAddress.next().toIpPrefix())
                }
            } else {
                val inet4RouteAddress = options.inet4RouteRange
                if (inet4RouteAddress.hasNext()) {
                    while (inet4RouteAddress.hasNext()) {
                        val address = inet4RouteAddress.next()
                        builder.addRoute(address.address(), address.prefix())
                    }
                }

                val inet6RouteAddress = options.inet6RouteRange
                if (inet6RouteAddress.hasNext()) {
                    while (inet6RouteAddress.hasNext()) {
                        val address = inet6RouteAddress.next()
                        builder.addRoute(address.address(), address.prefix())
                    }
                }
            }

            val includePackage = options.includePackage
            if (includePackage.hasNext()) {
                while (includePackage.hasNext()) {
                    try {
                        val nextPackage = includePackage.next()
                        builder.addAllowedApplication(nextPackage)
                        includeNames.append(nextPackage).append(' ')
                        Log.d("VPNService", "addAllowedApplication: $nextPackage")
                    } catch (e: NameNotFoundException) {
                        Log.e("VPNService", "addAllowedApplication failed", e)
                    }
                }
            }

            val excludePackage = options.excludePackage
            if (excludePackage.hasNext()) {
                while (excludePackage.hasNext()) {
                    try {
                        val nextPackage = excludePackage.next()
                        builder.addDisallowedApplication(nextPackage)
                        excludeNames.append(nextPackage).append(' ')
                        Log.d("VPNService", "addDisallowedApplication: $nextPackage")
                    } catch (e: NameNotFoundException) {
                        Log.e("VPNService", "addDisallowedApplication failed", e)
                    }
                }
            }
        }

        if (options.isHTTPProxyEnabled && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            systemProxyAvailable = true
            systemProxyEnabled = Settings.systemProxyEnabled
            if (systemProxyEnabled) {
                builder.setHttpProxy(
                    ProxyInfo.buildDirectProxy(
                        options.httpProxyServer,
                        options.httpProxyServerPort,
                        options.httpProxyBypassDomain.toList(),
                    ),
                )
            }
        } else {
            systemProxyAvailable = false
            systemProxyEnabled = false
        }

        EngineDiag.log(
            "tun",
            "builder include=[" + includeNames.toString().trim() + "] exclude=[" +
                excludeNames.toString().trim() + "] proxy=" + systemProxyEnabled,
        )
        val pfd =
            builder.establish() ?: error("android: the application is not prepared or is revoked")
        EngineDiag.log("tun", "established fd=" + pfd.fd + " mtu=" + tunMtu)
        EngineDiag.networkSnapshot("tun-up")
        service.fileDescriptor = pfd
        return pfd.fd
    }

    override fun sendNotification(notification: Notification) = service.sendNotification(notification)
}

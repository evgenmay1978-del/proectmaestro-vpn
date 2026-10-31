package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WhiteListAndroidBoundaryTest {
    @Test
    fun bindingSharesExistingListenerAndCreatesNoVpnService() {
        val binding = appFile("src/main/java/com/maestrovpn/tv/bg/WhiteListNetworkBinding.kt").readText()
        val manifest = appFile("src/main/AndroidManifest.xml").readText()

        assertTrue(binding.contains("DefaultNetworkListener.start"))
        assertTrue(binding.contains("DefaultNetworkListener.stop"))
        assertFalse(binding.contains("registerNetworkCallback"))
        assertFalse(binding.contains("requestNetwork"))
        assertFalse(binding.contains("VpnService()"))
        // VPN-интерфейс в системе один, поэтому сервисов ровно два и они взаимоисключающие:
        // рабочий sing-box (.bg.VPNService) и экспериментальный AIVPN (.aivpn.AivpnVpnService).
        // Intent-filter android.net.VpnService обязателен для каждого — без него система не
        // считает сервис VPN-сервисом. Инвариант поэтому не «одна строка в манифесте», а пара
        // «intent-filter ↔ сервис с BIND_VPN_SERVICE»: посторонний VPN-сервис не проскочит.
        assertEquals(2, Regex("android\\.net\\.VpnService").findAll(manifest).count())
        assertEquals(
            setOf(".bg.VPNService", ".aivpn.AivpnVpnService"),
            vpnServiceBodies(manifest).keys,
        )
        vpnServiceBodies(manifest).forEach { (name, body) ->
            assertTrue(
                "$name is declared as a VPN service but has no android.net.VpnService intent-filter",
                body.contains("android.net.VpnService"),
            )
        }
        assertFalse(manifest.contains("android:process"))
    }

    @Test
    fun dormantBindingIsNotActivatedByExistingServices() {
        listOf(
            appFile("src/main/java/com/maestrovpn/tv/bg/VPNService.kt"),
            appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt"),
        ).forEach { service ->
            assertFalse(service.readText().contains("WhiteListNetworkBinding"))
        }
    }

    @Test
    fun activeNativeSessionBelongsToExistingServiceAndUsesPrivateSameProcessSelection() {
        val session = appFile("src/main/java/com/maestrovpn/tv/whitelist/WhiteListSession.kt").readText()
        val box = appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText()
        val navigation = appFile("src/main/java/com/maestrovpn/tv/compose/navigation/SFANavigation.kt").readText()
        val manifest = appFile("src/main/AndroidManifest.xml").readText()
        assertTrue(box.contains("WhiteListSession(vpn)"))
        assertTrue(box.contains("whiteListSession?.prepare"))
        assertTrue(box.contains("whiteListSession?.destroy()"))
        assertTrue(box.contains("ContextCompat.RECEIVER_NOT_EXPORTED"))
        assertFalse(manifest.contains("android:process"))
        assertTrue(navigation.contains("groupsViewModel.selectCdn(tag)"))
        assertTrue(session.contains("DefaultNetworkListener.start"))
        assertTrue(session.contains("DefaultNetworkListener.stop"))
        // Границей владеет дочерний процесс (XhttpProcess): in-process protect(fd)/
        // ParcelFileDescriptor/bindSocket удалены, а собственный выход к CDN-эджу уведён
        // в direct правилом ip_cidr (WhiteListConfig.edgeDirectRoute).
        val config = appFile("src/main/java/com/maestrovpn/tv/whitelist/WhiteListConfig.kt").readText()
        assertTrue(session.contains("XhttpProcess.start"))
        assertTrue(session.contains("XhttpProcess.stop"))
        assertTrue(config.contains("edgeDirectRoute"))
        assertFalse(config.contains("VpnService()"))
        assertFalse(session.contains("adoptFd"))
        // Единственный собственный колбэк сессии — сторож Wi-Fi: платный CDN снимается, как только
        // появляется Wi-Fi (APK 159, 7403f2a). Граница: ровно одна регистрация, только на
        // TRANSPORT_WIFI, и она же снимается в stopWatchingWifi — никаких других колбэков сети.
        assertEquals(1, Regex("""(?<!un)registerNetworkCallback\(""").findAll(session).count())
        assertEquals(1, Regex("""unregisterNetworkCallback\(""").findAll(session).count())
        assertTrue(session.contains("addTransportType(NetworkCapabilities.TRANSPORT_WIFI)"))
        assertFalse(session.contains("VpnService()"))
    }

    /**
     * Services that opt into the system VPN role: android:name → opening tag + intent-filter body.
     * Deliberately not a string count — the manifest gains a service whenever a second engine is
     * added, and the boundary we care about is "who may own the single VPN interface".
     */
    private fun vpnServiceBodies(manifest: String): Map<String, String> =
        Regex("""<service\b([^>]*?)(?:/>|>([\s\S]*?)</service>)""").findAll(manifest)
            .mapNotNull { match ->
                val head = match.groupValues[1]
                if (!head.contains("android.permission.BIND_VPN_SERVICE")) return@mapNotNull null
                val name = Regex("""android:name="([^"]+)"""").find(head)?.groupValues?.get(1)
                    ?: error("VPN service without android:name")
                name to (head + match.groupValues[2])
            }
            .toMap()

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

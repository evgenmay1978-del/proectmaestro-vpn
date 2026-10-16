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
        assertEquals(1, Regex("android\\.net\\.VpnService").findAll(manifest).count())
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
        assertTrue(session.contains("if (!vpn.protect(fd)) false"))
        assertTrue(session.contains("ParcelFileDescriptor.fromFd(fd)"))
        assertTrue(session.contains("bindSocket(it.fileDescriptor)"))
        assertFalse(session.contains("adoptFd"))
        assertFalse(session.contains("registerNetworkCallback"))
        assertFalse(session.contains("VpnService()"))
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

package com.maestrovpn.tv.vendor

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D5 contract: a SUCCESSFUL OTA must not leave autostart disabled.
 *
 * The install path stops the tunnel (stopServiceIfRunning -> BoxService.stop -> stopService),
 * and stopService() clears Settings.startedByUser (BoxService.kt:469) because a teardown is
 * indistinguishable from a user stop. On a successful install the system kills the process at
 * the package-session commit and delivers MY_PACKAGE_REPLACED; BootReceiver then autostarts the
 * VPN only when Settings.startedByUser is true. Without restoring the flag before handing over
 * to the installer, every successful OTA silently disables «after reboot / after update connect»
 * (owner report: «после обновления VPN сам не поднимается»).
 *
 * The flag is restored conditionally — only when WE stopped a tunnel the user had started
 * (shouldRestoreTunnel, the same predicate the failed-install recovery already uses).
 */
class ApkInstallerAutostartFlagContractTest {
    @Test
    fun successfulInstallRestoresStartedByUserAfterStoppingTunnel() {
        val source = appFile("src/other/java/com/maestrovpn/tv/vendor/ApkInstaller.kt").readText()
        val install = installBody(source)

        val stop = install.indexOf("stopServiceIfRunning()")
        val restore = install.indexOf("Settings.startedByUser = true")
        val handover = install.indexOf("when (method)")
        require(stop >= 0 && handover >= 0) { "cannot locate the install hand-over in ApkInstaller.kt" }

        assertTrue("install() must restore Settings.startedByUser after the stop (D5)", restore > stop)
        assertTrue("the restore must happen BEFORE the installer is handed the APK", restore < handover)
        assertTrue("the restore must stay conditional on shouldRestoreTunnel", install.contains("if (shouldRestoreTunnel) Settings.startedByUser = true"))
    }

    /** Body of the install() function: from its declaration to the next top-level fun. */
    private fun installBody(source: String): String {
        val start = source.indexOf("suspend fun install(")
        require(start >= 0) { "install() is gone — update this contract" }
        val end = source.indexOf("fun canSystemSilentInstall", start)
        require(end > start) { "cannot delimit install()" }
        return source.substring(start, end)
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

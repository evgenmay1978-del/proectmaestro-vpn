package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D1 contract: when libbox reports that the box is gone (serviceStop), the service must publish
 * Stopped — not Starting.
 *
 * Starting here was inherited from upstream (io.nekohasekai/sfa bg/BoxService.kt:183), where the
 * engine is immediately re-created after a self-stop. MaestroVPN never re-creates it, so Starting
 * was a dead end: the UI stuck on «Подключение…» forever, stopService() could not follow (it only
 * exits on Stopping/Stopped), and every later start was ignored by the onStartCommand guard
 * (status != Stopped → return). Reproduced by owner report: «Залипает в Подключение…».
 */
class BoxServiceStatusContractTest {
    @Test
    fun serviceStopReportsStoppedNotStarting() {
        val body = serviceStopBody(appFile("src/main/java/com/maestrovpn/tv/bg/BoxService.kt").readText())

        assertTrue("serviceStop must publish Status.Stopped", body.contains("postValue(Status.Stopped)"))
        assertFalse("serviceStop must not publish Status.Starting", body.contains("postValue(Status.Starting)"))
    }

    /** The serviceStop() body: from its declaration to the next override. */
    private fun serviceStopBody(source: String): String {
        val start = source.indexOf("override fun serviceStop()")
        require(start >= 0) { "serviceStop() is gone — update this contract" }
        val end = source.indexOf("override fun serviceReload()", start)
        require(end > start) { "cannot delimit serviceStop()" }
        return source.substring(start, end)
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

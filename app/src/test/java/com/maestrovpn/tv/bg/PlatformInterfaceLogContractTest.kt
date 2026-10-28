package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * «Owner unknown» is the ordinary answer for sockets the kernel cannot attribute, and libbox expects
 * an exception there — but 1.0.181 answered each one with a full stack trace on System.err, which
 * buried real failures in logcat (found on the TV box by adb, 28.10.2026).
 */
class PlatformInterfaceLogContractTest {
    @Test
    fun anUnresolvedOwnerNeverPrintsAStackTrace() {
        assertTrue("a stack trace per socket is logcat noise, not diagnostics", !source().contains("printStackTrace"))
    }

    @Test
    fun theOwnerUnknownAnswerStillReachesTheEngine() {
        val body = section(source(), "override fun findConnectionOwner(", "override fun startDefaultInterfaceMonitor")
        assertTrue("the libbox contract is an exception, it must stay", body.contains("throw e"))
        assertTrue("the log must be throttled, not per socket", body.contains("UNRESOLVED_OWNER_LOG_EVERY"))
        assertEquals("the exception itself must keep its meaning", 1, body.windowed("throw e".length).count { it == "throw e" })
    }

    private fun section(source: String, from: String, to: String): String {
        val start = source.indexOf(from)
        require(start >= 0) { "missing " + from + " — update this contract" }
        val end = source.indexOf(to, start)
        require(end > start) { "cannot delimit " + from }
        return source.substring(start, end)
    }

    private fun source(): String {
        val path = "src/main/java/com/maestrovpn/tv/bg/PlatformInterfaceWrapper.kt"
        val found = listOf(File(path), File("app", path)).filter(File::isFile)
        require(found.size == 1) { "expected one wrapper source, found " + found.size }
        return found.single().readText()
    }
}

package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Our own servers are kept out of «Авто» by AutoSelectPool in BoxService.applySelectedContent. That
 * is only a guarantee while it is the one road to the engine: a second place that hands a config to
 * startOrReloadService would put our servers back into «Авто» through the back door.
 */
class AutoSelectWiringContractTest {
    @Test fun theEngineReceivesConfigsFromOnePlaceOnly() {
        val callers = sources().filter { it.readText().contains("startOrReloadService(") }
        assertEquals(listOf("BoxService.kt"), callers.map { it.name })
        assertEquals(1, Regex("startOrReloadService\\(").findAll(boxService()).count())
    }

    @Test fun autoPoolRunsBeforeTheConfigReachesTheEngine() {
        val source = boxService()
        val poolAt = source.indexOf("AutoSelectPool.exclude(base)")
        val patchAt = source.indexOf("SubXhttpRuntime.patchDirectRules(pool.config)")
        val handOverAt = source.indexOf("commandServer.startOrReloadService(withIpv6")
        assertTrue("the pool must be applied in the apply path", poolAt > 0)
        assertTrue("the pooled config must be the one patched and handed over", patchAt > poolAt && handOverAt > patchAt)
        assertTrue("the unpooled config must not reach the patch step", !source.contains("patchDirectRules(base)"))
    }

    private fun boxService(): String = sources().single { it.name == "BoxService.kt" }.readText()

    private fun sources(): List<File> {
        val root = listOf(File("src/main/java"), File("app/src/main/java")).single(File::isDirectory)
        return root.walkTopDown().filter { it.isFile && (it.extension == "kt" || it.extension == "java") }.toList()
    }
}

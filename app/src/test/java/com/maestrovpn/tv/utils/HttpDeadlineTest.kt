package com.maestrovpn.tv.utils

import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D13 contract: the update channel always returns.
 *
 * Libbox's HTTPClient sets no request timeout, and the panel is known to "complete TLS and then
 * stall". Before this fix PanelUpdateChecker / GitHubUpdateChecker ran request.execute() directly,
 * so one stalled host hung UpdateWorker (every 6 h) and every onResume check forever.
 *
 * Pinned here: a stalled call returns at its deadline (and closes the client), the blocking native
 * call never runs on the caller's thread, exceptions keep their identity (the runCatching fallbacks
 * in Vendor / UpdateWorker depend on it), and both checkers route their public entry point through
 * the deadline helper.
 */
class HttpDeadlineTest {
    @Test
    fun fastCallReturnsItsValue() {
        assertEquals("ok", withHttpDeadline(2_000) { "ok" })
    }

    @Test
    fun stalledCallReturnsNullAtTheDeadlineAndRunsTheTimeoutAction() {
        val timedOut = AtomicBoolean(false)
        val release = CountDownLatch(1)
        try {
            val startedAt = System.nanoTime()
            val value = withHttpDeadline(150, { timedOut.set(true) }) {
                // Stands in for a native request that ignores cancellation and never answers.
                release.await(5, TimeUnit.SECONDS)
                "late"
            }
            val elapsedMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startedAt)
            assertNull("a stalled call must not deliver its late value", value)
            assertTrue("onTimeout must run so the caller can close its client", timedOut.get())
            assertTrue("must return at the deadline, was ${elapsedMs} ms", elapsedMs in 100..3_000)
        } finally {
            release.countDown()
        }
    }

    @Test
    fun blockRunsOffTheCallerThread() {
        val caller = Thread.currentThread()
        val worker = AtomicReference<Thread?>(null)
        withHttpDeadline(2_000) {
            worker.set(Thread.currentThread())
            "ok"
        }
        val actual = worker.get()
        assertNotNull("block must have run", actual)
        assertNotSame("a blocking native call must not run on the caller thread", caller, actual)
    }

    @Test
    fun exceptionIsRethrownUnchanged() {
        val boom = IllegalStateException("boom")
        val thrown = assertThrows(IllegalStateException::class.java) {
            withHttpDeadline<String>(2_000) { throw boom }
        }
        assertSame("runCatching() fallbacks rely on the original exception type", boom, thrown)
    }

    @Test
    fun nullResultIsAllowed() {
        assertNull(withHttpDeadline<String>(2_000) { null })
    }

    @Test
    fun failingTimeoutActionStillReturns() {
        val release = CountDownLatch(1)
        try {
            val value = withHttpDeadline(150, { throw IllegalStateException("close failed") }) {
                release.await(5, TimeUnit.SECONDS)
                "late"
            }
            assertNull("a failing close() must not escape the deadline handler", value)
        } finally {
            release.countDown()
        }
    }

    @Test
    fun bothUpdateCheckersRouteTheirEntryPointThroughTheDeadline() {
        val panel = appFile("src/github/java/com/maestrovpn/tv/vendor/PanelUpdateChecker.kt").readText()
        // The raw request lives in a private body that only the bounded entry point calls.
        assertTrue(
            "PanelUpdateChecker: the public check must run under the deadline",
            panel.contains("withHttpDeadline(CHECK_TIMEOUT_MS, ::close)"),
        )
        assertTrue("PanelUpdateChecker: raw request body must stay private", panel.contains("private fun fetchManifest("))
        assertTrue("PanelUpdateChecker: one deadline, no second unbounded path", Regex("withHttpDeadline\\(").findAll(panel).count() == 1)
        // The timeout path closes the client while the caller's use { } closes it again.
        assertTrue(
            "PanelUpdateChecker: close() must be idempotent",
            panel.contains("if (closed.compareAndSet(false, true)) client.close()"),
        )

        val github = appFile("src/github/java/com/maestrovpn/tv/vendor/GitHubUpdateChecker.kt").readText()
        assertTrue(
            "GitHubUpdateChecker: the public check must run under the deadline",
            github.contains("withHttpDeadline(CHECK_TIMEOUT_MS, ::close)"),
        )
        assertTrue("GitHubUpdateChecker: raw request body must stay private", github.contains("private fun checkUpdateUnbounded("))
        assertTrue("GitHubUpdateChecker: one deadline, no second unbounded path", Regex("withHttpDeadline\\(").findAll(github).count() == 1)
        assertTrue(
            "GitHubUpdateChecker: close() must be idempotent",
            github.contains("if (closed.compareAndSet(false, true)) client.close()"),
        )
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

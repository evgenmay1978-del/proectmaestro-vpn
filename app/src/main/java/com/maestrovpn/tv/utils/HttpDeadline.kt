package com.maestrovpn.tv.utils

import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

/**
 * Bound a control-plane call that may block forever on a stalled peer.
 *
 * libbox's HTTPClient sets no request timeout (see the "completes TLS and then stalls" note in
 * [httpFetchSubscription]): a host that accepts TCP/TLS and then goes silent hangs the caller
 * forever. For the update channel that meant UpdateWorker (every 6 h) never finishing its work and
 * onResume checks never returning (D13 audit 28.10.2026).
 *
 * The native call is blocking and ignores coroutine cancellation, so it runs on a detached daemon
 * thread and only the WAIT is bounded. On timeout [onTimeout] is invoked — callers pass their
 * client's close(), which aborts the in-flight request — and null is returned. Exceptions raised by
 * [block] are rethrown unchanged, so existing runCatching fallbacks keep their semantics.
 */
internal fun <T> withHttpDeadline(timeoutMs: Long, onTimeout: () -> Unit = {}, block: () -> T?): T? {
    val result = CompletableFuture<T?>()
    deadlineExecutor.execute {
        try {
            result.complete(block())
        } catch (t: Throwable) {
            result.completeExceptionally(t)
        }
    }
    return try {
        result.get(timeoutMs, TimeUnit.MILLISECONDS)
    } catch (e: TimeoutException) {
        runCatching { onTimeout() }
        null
    } catch (e: ExecutionException) {
        throw e.cause ?: e
    } catch (e: InterruptedException) {
        Thread.currentThread().interrupt()
        null
    }
}

/** Daemon pool: a stalled native request must never keep the process alive. */
private val deadlineExecutor = Executors.newCachedThreadPool { runnable ->
    Thread(runnable, "http-deadline").apply { isDaemon = true }
}

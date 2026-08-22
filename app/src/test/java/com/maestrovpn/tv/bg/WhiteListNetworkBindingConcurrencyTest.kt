package com.maestrovpn.tv.bg

import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class WhiteListNetworkBindingConcurrencyTest {
    @Test
    fun concurrentStartsRegisterExactlyOnce() = runBlocking {
        val entered = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val starts = AtomicInteger()
        val binding = WhiteListNetworkBinding(
            startListener = { _, _ ->
                starts.incrementAndGet()
                entered.complete(Unit)
                release.await()
            },
            stopListener = { _ -> },
        )

        val first = launch { binding.start { } }
        entered.await()
        val second = launch { binding.start { } }
        yield()
        assertFalse(second.isCompleted)

        release.complete(Unit)
        joinAll(first, second)
        assertEquals(1, starts.get())
    }

    @Test
    fun stopQueuedBehindSuspendedStartCannotLeakRegistration() = runBlocking {
        val entered = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val calls = mutableListOf<String>()
        val binding = WhiteListNetworkBinding(
            startListener = { _, _ ->
                calls += "start"
                entered.complete(Unit)
                release.await()
            },
            stopListener = { _ -> calls += "stop" },
        )

        val start = launch { binding.start { } }
        entered.await()
        val stop = launch { binding.stop() }
        yield()
        assertFalse(stop.isCompleted)

        release.complete(Unit)
        joinAll(start, stop)
        assertEquals(listOf("start", "stop"), calls)
    }
}

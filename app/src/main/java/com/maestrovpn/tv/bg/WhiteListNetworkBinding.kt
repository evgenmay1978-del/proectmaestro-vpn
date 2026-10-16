package com.maestrovpn.tv.bg

import android.net.Network
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

internal class WhiteListNetworkBinding(
    private val key: Any = Any(),
    private val startListener: suspend (Any, (Network?) -> Unit) -> Unit = { listenerKey, listener ->
        DefaultNetworkListener.start(listenerKey, listener)
    },
    private val stopListener: suspend (Any) -> Unit = { listenerKey ->
        DefaultNetworkListener.stop(listenerKey)
    },
) {
    private val lifecycle = Mutex()
    private var started = false

    suspend fun start(listener: (Network?) -> Unit) {
        lifecycle.withLock {
            if (started) return@withLock
            startListener(key, listener)
            started = true
        }
    }

    suspend fun stop() {
        lifecycle.withLock {
            if (!started) return@withLock
            stopListener(key)
            started = false
        }
    }
}

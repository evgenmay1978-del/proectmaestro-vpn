package com.maestrovpn.tv.bg

import android.net.Network
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

internal class WhiteListNetworkBinding(
    private val key: Any = Any(),
) {
    private val lifecycle = Mutex()
    private var started = false

    suspend fun start(listener: (Network?) -> Unit) {
        lifecycle.withLock {
            check(!started)
            DefaultNetworkListener.start(key, listener)
            started = true
        }
    }

    suspend fun stop() {
        lifecycle.withLock {
            if (!started) return@withLock
            DefaultNetworkListener.stop(key)
            started = false
        }
    }
}

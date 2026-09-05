package com.maestrovpn.tv.whitelist

import androidx.annotation.Keep

@Keep
internal object XhttpNative {
    private val available: Boolean by lazy {
        try { System.loadLibrary("maestro_xhttp"); true } catch (_: LinkageError) { false }
    }
    fun available(): Boolean = available
    @JvmStatic external fun nativeStart(sessionId: Long, payload: ByteArray, protector: SocketProtector): Int
    @JvmStatic external fun nativeStop(sessionId: Long): Int
}

@Keep
internal interface SocketProtector {
    @Keep fun protectSocket(sessionId: Long, fd: Int): Boolean
}

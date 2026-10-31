package com.aivpn.client

import android.net.VpnService
import android.util.Log

/**
 * JNI bridge to the AIVPN native core (libaivpn_core.so, Rust).
 *
 * IMPORTANT: the class name and package are NOT a style choice. The exported symbols in the
 * published .so are mangled as Java_com_aivpn_client_AivpnJni_<method>, so this object must
 * stay exactly at com.aivpn.client.AivpnJni — renaming or moving it turns every call into
 * UnsatisfiedLinkError.
 *
 * The library is optional at runtime: without the .so (fetch it with tools/fetch-aivpn-so.sh)
 * [isAvailable] reports false and the rest of the app keeps working on the sing-box path.
 */
object AivpnJni {
    private const val TAG = "AivpnJni"

    /**
     * Non-null when libaivpn_core.so failed to load (missing ABI, stripped install).
     *
     * System.loadLibrary throws [UnsatisfiedLinkError], which is an [Error], not an [Exception] —
     * an unguarded load would turn the first touch of this object into
     * ExceptionInInitializerError, which no catch(Exception) can intercept.
     */
    @Volatile
    var loadError: String? = null
        private set

    val isAvailable: Boolean
        get() = loadError == null

    init {
        try {
            System.loadLibrary("aivpn_core")
        } catch (t: Throwable) { // UnsatisfiedLinkError is an Error
            loadError = t.javaClass.simpleName + ": " + t.message
            Log.e(TAG, "libaivpn_core.so is not available", t)
        }
    }

    /**
     * Runs a full tunnel session on the calling thread (blocks until the session ends).
     *
     * The native side dup(2)s [tunFd] and only closes its own duplicate — the caller keeps
     * ownership of the ParcelFileDescriptor. The UDP socket is protected from the tunnel by the
     * native code calling [VpnService.protect] on this instance.
     *
     * @return empty string on a clean exit, an error message otherwise.
     */
    external fun runTunnel(
        vpnService: VpnService,
        tunFd: Int,
        serverHost: String,
        serverPort: Int,
        serverKey: ByteArray,
        psk: ByteArray?,
        mtlsCert: ByteArray?,
        adaptiveLevel: Int,
        staticPrivkey: ByteArray?,
        maskProfile: String?,
        serverSigningKey: ByteArray?,
        maskOperatorPubkey: ByteArray?,
        maskVerifyMode: Int,
        polymorphicBase: String?,
        shareMaskFeedback: Boolean,
        receiveMaskHints: Boolean,
        countryCode: String?,
        priorOutcomesJson: String?,
        cachedDescriptorsJson: String?,
        transportName: String?,
        transportParamsJson: String?,
    ): String

    /** Signals the blocking [runTunnel] call to return. Safe to call when no session is active. */
    external fun stopTunnel()

    /**
     * Clears the pending-stop flag that [stopTunnel] leaves behind. A start right after a stop
     * would otherwise exit immediately (the flag survives the previous session).
     */
    external fun clearPendingStop()

    /** Signed bootstrap descriptors persisted by a previous session, as a JSON array. */
    external fun getBootstrapDescriptorsJson(): String
}

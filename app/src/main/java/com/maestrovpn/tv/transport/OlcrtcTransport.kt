package com.maestrovpn.tv.transport

import com.maestrovpn.olcrtc.mobile.Mobile

/**
 * olcRTC — an opt-in WebRTC video-disguise fallback transport (the AmneziaWG slot).
 *
 * This is the integration anchor: it links the olcRTC gomobile binding (com.maestrovpn.olcrtc.*)
 * into the APK so the build proves olcrtc.aar coexists with libbox.aar (different native libs:
 * libgojni.so vs libbox.so; -javapkg-namespaced classes). The real wiring lands next:
 *   - a [com.maestrovpn.olcrtc.mobile.SocketProtector] backed by VpnService.protect(),
 *   - Mobile.start(carrier, room, client, keyHex, socksPort, user, pass) -> a local SOCKS5,
 *   - a sing-box SOCKS5 outbound pointing at 127.0.0.1:<socksPort>, selectable as a "protocol".
 */
internal object OlcrtcTransport {
    /** Present so the linker keeps the binding; returns the olcRTC API class name. */
    fun bindingClass(): String = Mobile::class.java.name
}

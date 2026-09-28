package com.maestrovpn.tv.utils

import android.os.Build
import java.net.InetAddress
import java.net.Socket
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SNIHostName
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

/**
 * Dial a literal CDN edge address while still presenting the CDN origin's name on the wire.
 *
 * Without SNI the edge cannot select our certificate, and without the verifier below the
 * certificate would be checked against the IP. Both are required by the pinned-edge fallback,
 * used when a mobile operator's whitelist carries a CDN CIDR that its own DNS/GSLB does not hand
 * out. Available from API 24; older devices keep the plain DNS path.
 */
internal fun HttpsURLConnection.pinServerName(name: String) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return
    val defaultVerifier: HostnameVerifier = HttpsURLConnection.getDefaultHostnameVerifier()
    val delegate: SSLSocketFactory = HttpsURLConnection.getDefaultSSLSocketFactory()
    sslSocketFactory = object : SSLSocketFactory() {
        override fun getDefaultCipherSuites(): Array<String> = delegate.defaultCipherSuites
        override fun getSupportedCipherSuites(): Array<String> = delegate.supportedCipherSuites
        override fun createSocket(s: Socket, host: String, port: Int, autoClose: Boolean): Socket {
            val socket = delegate.createSocket(s, host, port, autoClose)
            if (socket is SSLSocket) {
                val parameters = socket.sslParameters
                parameters.serverNames = listOf(SNIHostName(name))
                socket.sslParameters = parameters
            }
            return socket
        }
        override fun createSocket(host: String, port: Int): Socket = delegate.createSocket(host, port)
        override fun createSocket(host: String, port: Int, localHost: String, localPort: Int): Socket =
            delegate.createSocket(host, port, localHost, localPort)
        override fun createSocket(host: InetAddress, port: Int): Socket = delegate.createSocket(host, port)
        override fun createSocket(address: InetAddress, port: Int, localAddress: InetAddress, localPort: Int): Socket =
            delegate.createSocket(address, port, localAddress, localPort)
    }
    hostnameVerifier = HostnameVerifier { _, session -> defaultVerifier.verify(name, session) }
}

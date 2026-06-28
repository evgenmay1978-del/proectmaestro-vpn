package com.maestrovpn.tv.bg

import android.net.DnsResolver
import android.os.Build
import android.os.CancellationSignal
import android.system.ErrnoException
import androidx.annotation.RequiresApi
import io.nekohasekai.libbox.ExchangeContext
import io.nekohasekai.libbox.LocalDNSTransport
import com.maestrovpn.tv.ktx.tryResumeWithException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.asExecutor
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import java.net.InetAddress
import java.net.UnknownHostException
import kotlin.coroutines.resume
import kotlin.coroutines.suspendCoroutine

object LocalResolver : LocalDNSTransport {
    private const val RCODE_NXDOMAIN = 3
    private const val RCODE_SERVFAIL = 2

    // Must stay < BoxService.STOP_CLOSE_TIMEOUT (3000ms) so a hung upstream DNS
    // resolve can never block the libbox close path past the bounded stop window.
    private const val DNS_TIMEOUT_MS = 2500L

    override fun raw(): Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q

    @RequiresApi(Build.VERSION_CODES.Q)
    override fun exchange(ctx: ExchangeContext, message: ByteArray) {
        val defaultNetwork = DefaultNetworkMonitor.defaultNetwork ?: error("missing default interface")
        return runBlocking {
            val result = withTimeoutOrNull(DNS_TIMEOUT_MS) {
            suspendCoroutine { continuation ->
                val signal = CancellationSignal()
                ctx.onCancel(signal::cancel)
                val callback =
                    object : DnsResolver.Callback<ByteArray> {
                        override fun onAnswer(answer: ByteArray, rcode: Int) {
                            if (rcode == 0) {
                                ctx.rawSuccess(answer)
                            } else {
                                ctx.errorCode(rcode)
                            }
                            continuation.resume(Unit)
                        }

                        override fun onError(error: DnsResolver.DnsException) {
                            when (val cause = error.cause) {
                                is ErrnoException -> {
                                    ctx.errnoCode(cause.errno)
                                    continuation.resume(Unit)
                                    return
                                }
                            }
                            continuation.tryResumeWithException(error)
                        }
                    }
                DnsResolver.getInstance().rawQuery(
                    defaultNetwork,
                    message,
                    DnsResolver.FLAG_NO_RETRY,
                    Dispatchers.IO.asExecutor(),
                    signal,
                    callback,
                )
            }
            }
            if (result == null) {
                // Timed out — fail closed with SERVFAIL instead of blocking the close path.
                ctx.errorCode(RCODE_SERVFAIL)
            }
        }
    }

    override fun lookup(ctx: ExchangeContext, network: String, domain: String) {
        val defaultNetwork = DefaultNetworkMonitor.defaultNetwork ?: error("missing default interface")
        return runBlocking {
            val result = withTimeoutOrNull(DNS_TIMEOUT_MS) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                suspendCoroutine { continuation ->
                    val signal = CancellationSignal()
                    ctx.onCancel(signal::cancel)
                    val callback =
                        object : DnsResolver.Callback<Collection<InetAddress>> {
                            @Suppress("ThrowableNotThrown")
                            override fun onAnswer(answer: Collection<InetAddress>, rcode: Int) {
                                if (rcode == 0) {
                                    ctx.success(
                                        (answer as Collection<InetAddress?>).mapNotNull { it?.hostAddress }
                                            .joinToString("\n"),
                                    )
                                } else {
                                    ctx.errorCode(rcode)
                                }
                                continuation.resume(Unit)
                            }

                            override fun onError(error: DnsResolver.DnsException) {
                                when (val cause = error.cause) {
                                    is ErrnoException -> {
                                        ctx.errnoCode(cause.errno)
                                        continuation.resume(Unit)
                                        return
                                    }
                                }
                                continuation.tryResumeWithException(error)
                            }
                        }
                    val type =
                        when {
                            network.endsWith("4") -> DnsResolver.TYPE_A
                            network.endsWith("6") -> DnsResolver.TYPE_AAAA
                            else -> null
                        }
                    if (type != null) {
                        DnsResolver.getInstance().query(
                            defaultNetwork,
                            domain,
                            type,
                            DnsResolver.FLAG_NO_RETRY,
                            Dispatchers.IO.asExecutor(),
                            signal,
                            callback,
                        )
                    } else {
                        DnsResolver.getInstance().query(
                            defaultNetwork,
                            domain,
                            DnsResolver.FLAG_NO_RETRY,
                            Dispatchers.IO.asExecutor(),
                            signal,
                            callback,
                        )
                    }
                }
            } else {
                val answer =
                    try {
                        defaultNetwork.getAllByName(domain)
                    } catch (e: UnknownHostException) {
                        ctx.errorCode(RCODE_NXDOMAIN)
                        return@withTimeoutOrNull
                    }
                ctx.success(answer.mapNotNull { it.hostAddress }.joinToString("\n"))
            }
            }
            if (result == null) {
                // Timed out — fail closed with SERVFAIL instead of blocking the close path.
                ctx.errorCode(RCODE_SERVFAIL)
            }
        }
    }
}

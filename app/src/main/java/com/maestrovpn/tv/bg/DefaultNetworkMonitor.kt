package com.maestrovpn.tv.bg

import android.net.Network
import android.os.Build
import io.nekohasekai.libbox.InterfaceUpdateListener
import com.maestrovpn.tv.Application
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import java.net.NetworkInterface

object DefaultNetworkMonitor {

    @Volatile
    var defaultNetwork: Network? = null

    @Volatile
    private var listener: InterfaceUpdateListener? = null

    suspend fun start() {
        DefaultNetworkListener.start(this) {
            defaultNetwork = it
            checkDefaultInterfaceUpdate(it)
        }
        defaultNetwork = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            Application.connectivity.activeNetwork
        } else {
            DefaultNetworkListener.get()
        }
    }

    suspend fun stop() {
        DefaultNetworkListener.stop(this)
    }

    suspend fun require(): Network {
        val network = defaultNetwork
        if (network != null) {
            return network
        }
        return DefaultNetworkListener.get()
    }

    fun setListener(listener: InterfaceUpdateListener?) {
        this.listener = listener
        checkDefaultInterfaceUpdate(defaultNetwork)
    }

    private fun checkDefaultInterfaceUpdate(newNetwork: Network?) {
        val listener = listener ?: return
        if (newNetwork != null) {
            // The retry waits used to be Thread.sleep on the libbox callback / main thread.
            // Off-load to Dispatchers.IO so the polling never blocks those threads (delay()
            // instead of sleep()). Behavior/return is unchanged.
            runBlocking(Dispatchers.IO) {
                for (times in 0 until 10) {
                    val linkProperties = Application.connectivity.getLinkProperties(newNetwork)
                    if (linkProperties == null) {
                        delay(100)
                        continue
                    }
                    var interfaceIndex: Int
                    try {
                        interfaceIndex = NetworkInterface.getByName(linkProperties.interfaceName).index
                    } catch (e: Exception) {
                        delay(100)
                        continue
                    }
                    listener.updateDefaultInterface(linkProperties.interfaceName, interfaceIndex, false, false)
                    // Resolved successfully — stop. Without this break the loop ran all 10 times,
                    // pushing the same interface update into libbox 10× per network change for no
                    // benefit (upstream lacks the break too; this is a deliberate improvement).
                    break
                }
            }
        } else {
            listener.updateDefaultInterface("", -1, false, false)
        }
    }
}

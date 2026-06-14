package com.maestrovpn.tv.xposed

import android.content.Context
import com.maestrovpn.tv.xposed.hooks.HookIConnectivityManagerOnTransact
import com.maestrovpn.tv.xposed.hooks.hidevpn.ConnectivityServiceHookHelper
import com.maestrovpn.tv.xposed.hooks.hidevpn.HookNetworkCapabilitiesWriteToParcel
import com.maestrovpn.tv.xposed.hooks.hidevpn.HookNetworkInterfaceGetName
import com.maestrovpn.tv.xposed.hooks.hidevpnapp.HookPackageManagerGetInstalledPackages

object HookInstaller {

    private const val TAG = "XposedInit"

    private val activityThreadClass by lazy { Class.forName("android.app.ActivityThread") }
    private val currentActivityThreadMethod by lazy { activityThreadClass.getMethod("currentActivityThread") }
    private val getSystemContextMethod by lazy { activityThreadClass.getMethod("getSystemContext") }

    fun install(classLoader: ClassLoader) {
        val systemContext = resolveSystemContext()
        HookErrorStore.i(TAG, "handleSystemServerLoaded")
        val hooks = arrayOf(
            ConnectivityServiceHookHelper(classLoader),
            HookIConnectivityManagerOnTransact(classLoader, systemContext),
            HookPackageManagerGetInstalledPackages(classLoader),
            HookNetworkCapabilitiesWriteToParcel(),
            HookNetworkInterfaceGetName(classLoader),
        )

        hooks.forEach { hook ->
            try {
                hook.injectHook()
            } catch (e: Throwable) {
                HookErrorStore.e(
                    TAG,
                    "Failed to inject ${hook.javaClass.simpleName}",
                    e,
                )
            }
        }
    }

    private fun resolveSystemContext(): Context? = try {
        val currentThread = currentActivityThreadMethod.invoke(null)
        getSystemContextMethod.invoke(currentThread) as? Context
    } catch (e: Throwable) {
        HookErrorStore.e(TAG, "resolveSystemContext failed", e)
        null
    }
}

package com.maestrovpn.tv.utils

import android.content.Context
import android.os.RemoteException
import android.util.Log
import com.maestrovpn.tv.bg.PackageEntry
import com.maestrovpn.tv.bg.ParceledListSlice
import com.maestrovpn.tv.bg.RootClient
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.xposed.HookModuleVersion
import com.maestrovpn.tv.xposed.HookStatusKeys

object PrivilegeSettingsClient {
    private const val TAG = "PrivilegeSettingsClient"

    @Volatile
    private var appContext: Context? = null

    data class ExportResult(val outputPath: String?, val error: String?)

    fun register(context: Context) {
        appContext = context.applicationContext
        sync()
    }

    fun sync(): Throwable? {
        val context = appContext ?: return null
        if (isVersionMismatch()) return null
        val binder = ConnectivityBinderUtils.getBinder(context) ?: return null
        return ConnectivityBinderUtils.withParcel { data, reply ->
            data.writeInterfaceToken(HookStatusKeys.DESCRIPTOR)
            data.writeInt(if (Settings.privilegeSettingsEnabled) 1 else 0)
            ParceledListSlice(Settings.privilegeSettingsList.map { PackageEntry(it) }).writeToParcel(data, 0)
            data.writeInt(if (Settings.privilegeSettingsInterfaceRenameEnabled) 1 else 0)
            data.writeString(Settings.privilegeSettingsInterfacePrefix)
            try {
                val ok = binder.transact(HookStatusKeys.TRANSACTION_UPDATE_PRIVILEGE_SETTINGS, data, reply, 0)
                reply.readException()
                if (!ok) {
                    val error = RemoteException()
                    Log.w(TAG, "Privilege settings sync failed: transaction not handled", error)
                    return@withParcel error
                }
                return@withParcel null
            } catch (e: RemoteException) {
                Log.w(TAG, "Privilege settings sync failed: remote exception", e)
                return@withParcel e
            } catch (e: RuntimeException) {
                Log.w(TAG, "Privilege settings sync failed: bad reply", e)
                return@withParcel e
            }
        }
    }

    suspend fun exportDebugInfo(outputPath: String): ExportResult = try {
        val service = RootClient.bindService()
        val path = service.exportDebugInfo(outputPath)
        ExportResult(path, null)
    } catch (e: Throwable) {
        Log.e(TAG, "Export debug info failed", e)
        ExportResult(null, e.message ?: "export failed")
    }

    private fun isVersionMismatch(): Boolean {
        val status = HookStatusClient.status.value ?: return false
        return status.version != HookModuleVersion.CURRENT
    }
}

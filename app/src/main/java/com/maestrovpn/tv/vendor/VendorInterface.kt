package com.maestrovpn.tv.vendor

import android.app.Activity
import androidx.camera.core.ImageAnalysis
import com.maestrovpn.tv.compose.screen.qrscan.QRCodeCropArea
import com.maestrovpn.tv.update.UpdateInfo
import com.maestrovpn.tv.update.UpdateSource

interface VendorInterface {
    fun checkUpdate(activity: Activity, byUser: Boolean)

    fun createQRCodeAnalyzer(
        onSuccess: (String) -> Unit,
        onFailure: (Exception) -> Unit,
        onCropArea: ((QRCodeCropArea?) -> Unit)? = null,
    ): ImageAnalysis.Analyzer?

    fun isPerAppProxyAvailable(): Boolean = true

    val hasCustomUpdate: Boolean get() = false

    val updateSources: List<UpdateSource> get() = listOf(UpdateSource.GITHUB)

    fun checkUpdateAsync(): UpdateInfo? = null

    fun scheduleAutoUpdate() {}

    suspend fun verifySilentInstallMethod(method: String): Boolean = false

    suspend fun downloadAndInstall(context: android.content.Context, downloadUrl: String): Unit = throw UnsupportedOperationException("Not supported in this flavor")
}

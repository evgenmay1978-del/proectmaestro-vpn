package com.maestrovpn.tv.vendor

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.withTimeout
import java.io.File
import java.io.FileInputStream
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import android.content.pm.PackageInstaller as AndroidPackageInstaller

object SystemPackageInstaller {

    // Terminal result of one install session, delivered by InstallResultReceiver.
    data class InstallResult(val status: Int, val message: String?)

    // sessionId → waiter. The commit() callback is ASYNC — the old code returned right after
    // commit() and every caller treated that as "installed", so a failed install (signature
    // mismatch, downgrade, declined dialog) was silently swallowed and the app re-offered +
    // re-downloaded the same update forever. Now install() suspends until the real verdict.
    private val pending = ConcurrentHashMap<Int, CompletableDeferred<InstallResult>>()

    // A confirm dialog on a slow TV can sit unanswered for a while; past this we surface a
    // readable error instead of hanging the caller (WorkManager kills workers at 10 min).
    private const val RESULT_TIMEOUT_MS = 4 * 60 * 1000L

    fun canSystemSilentInstall(): Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S

    /** Called by InstallResultReceiver with the terminal status of a session. */
    fun onInstallResult(sessionId: Int, status: Int, message: String?) {
        pending.remove(sessionId)?.complete(InstallResult(status, message))
    }

    suspend fun install(context: Context, apkFile: File) {
        val packageInstaller = context.packageManager.packageInstaller
        val params = AndroidPackageInstaller.SessionParams(AndroidPackageInstaller.SessionParams.MODE_FULL_INSTALL)
        params.setAppPackageName(context.packageName)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            params.setRequireUserAction(AndroidPackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
        }

        val sessionId = packageInstaller.createSession(params)
        val waiter = CompletableDeferred<InstallResult>()
        pending[sessionId] = waiter
        try {
            packageInstaller.openSession(sessionId).use { session ->
                session.openWrite("update.apk", 0, apkFile.length()).use { outputStream ->
                    FileInputStream(apkFile).use { inputStream ->
                        inputStream.copyTo(outputStream)
                    }
                    session.fsync(outputStream)
                }

                val intent = Intent(context, InstallResultReceiver::class.java).apply {
                    action = InstallResultReceiver.ACTION_INSTALL_COMPLETE
                }
                val pendingIntent = PendingIntent.getBroadcast(
                    context,
                    sessionId,
                    intent,
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE,
                )

                session.commit(pendingIntent.intentSender)
            }

            // On SUCCESS the system replaces (kills) this process, so a successful install
            // usually never returns from here — that's fine, the new build starts fresh.
            val result = try {
                withTimeout(RESULT_TIMEOUT_MS) { waiter.await() }
            } catch (e: TimeoutCancellationException) {
                runCatching { packageInstaller.abandonSession(sessionId) }
                throw IOException(
                    "Установка не была подтверждена. Откройте приложение и подтвердите обновление.",
                )
            }
            if (result.status != AndroidPackageInstaller.STATUS_SUCCESS) {
                throw IOException(readableError(result))
            }
        } finally {
            pending.remove(sessionId)
        }
    }

    private fun readableError(result: InstallResult): String {
        val raw = result.message.orEmpty()
        return when {
            result.status == AndroidPackageInstaller.STATUS_FAILURE_ABORTED ->
                "Установка отменена пользователем"
            result.status == AndroidPackageInstaller.STATUS_FAILURE_STORAGE ->
                "Недостаточно места для установки обновления"
            result.status == AndroidPackageInstaller.STATUS_FAILURE_INCOMPATIBLE ||
                raw.contains("signature", ignoreCase = true) ||
                raw.contains("INCONSISTENT_CERTIFICATES", ignoreCase = true) ->
                "Подпись обновления не совпадает с установленным приложением — " +
                    "удалите приложение и установите последнюю версию заново"
            raw.contains("VERSION_DOWNGRADE", ignoreCase = true) ->
                "Загруженная версия старше установленной — обновление отклонено системой"
            raw.isNotBlank() -> "Не удалось установить обновление: $raw"
            else -> "Не удалось установить обновление (код ${result.status})"
        }
    }
}

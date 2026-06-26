package com.maestrovpn.tv.vendor

import com.maestrovpn.tv.Application
import com.maestrovpn.tv.update.UpdateState
import com.maestrovpn.tv.utils.HTTPClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import kotlin.coroutines.coroutineContext

/**
 * Resumable, verified APK downloader — built for slow/throttled Russian links.
 *
 * Root cause of "обновление не ставится / сбой": the previous downloader restarted the
 * ~95 MB file from byte 0 on every drop, had no timeouts or retries, and could hand a
 * PARTIAL file to the package installer. This one:
 *   - downloads to update.apk.part and RESUMES with an HTTP Range request on reconnect,
 *   - retries with exponential backoff on network errors (keeping the .part for resume),
 *   - sets connect/read timeouts so it never hangs forever on a stalled socket,
 *   - verifies size + sha256 (from the panel manifest) BEFORE finalizing,
 *   - only renames the verified file to update.apk, so the installer never sees junk.
 *
 * The panel (/update/) already serves HTTP Range; this is the client side that uses it.
 */
class ApkDownloader : Closeable {

    suspend fun download(url: String): File = withContext(Dispatchers.IO) {
        // noBackupFilesDir (NOT cacheDir): on 1 GB TVs Android evicts cacheDir under pressure, and
        // an ~85 MB file is a prime target — losing it mid-flight makes the download restart from 0
        // ("downloads, then starts over"). noBackupFilesDir is app-owned and not auto-purged, so the
        // .part survives across attempts and app restarts; it's excluded from cloud backup (correct
        // for a throwaway APK).
        val dlDir = File(Application.application.noBackupFilesDir, "updates").apply { mkdirs() }
        val apkFile = File(dlDir, "update.apk")
        val partFile = File(dlDir, "update.apk.part")

        val info = UpdateState.updateInfo.value
        val expectedSize = info?.fileSize ?: 0L
        val expectedSha = info?.sha256?.lowercase().orEmpty()

        // A complete, verified APK from a prior run → reuse it (no re-download).
        if (apkFile.exists() && verifies(apkFile, expectedSize, expectedSha)) {
            UpdateState.downloadProgress.value = 1f
            UpdateState.saveApkPath(apkFile)
            return@withContext apkFile
        }
        if (apkFile.exists()) apkFile.delete()

        // Identity guard: a leftover .part may belong to a DIFFERENT release. When the fleet saw
        // 1.0.100 then 1.0.101 in quick succession, a client mid-download had a .part for the old
        // APK; resuming it appends the NEW APK's bytes onto a stale prefix → length can reach
        // expectedSize while the sha256 never matches → delete → re-download → the reported
        // "downloads to 100%, then starts over" loop. Tag the .part with this target's identity;
        // if a leftover .part doesn't match, discard it and start clean (resume of a truncated —
        // not cross-version — .part still works, because same target ⇒ same id).
        val partId = File(dlDir, "update.apk.part.id")
        val wantId = "$expectedSize:$expectedSha"
        if (wantId != "0:" && partFile.exists() &&
            (!partId.exists() || partId.readText().trim() != wantId)) {
            partFile.delete()
        }
        runCatching { partId.writeText(wantId) }

        var lastError: Exception? = null
        repeat(MAX_ATTEMPTS) { attempt ->
            coroutineContext.ensureActive()
            try {
                streamWithResume(url, partFile, expectedSize)

                if (expectedSize > 0 && partFile.length() != expectedSize) {
                    throw IOException("size ${partFile.length()} != expected $expectedSize")
                }
                if (expectedSha.isNotBlank()) {
                    val got = sha256Of(partFile)
                    if (!got.equals(expectedSha, ignoreCase = true)) {
                        partFile.delete() // corrupt bytes — start fresh on the next attempt
                        throw IOException("sha256 mismatch")
                    }
                }

                if (apkFile.exists()) apkFile.delete()
                if (!partFile.renameTo(apkFile)) {
                    partFile.copyTo(apkFile, overwrite = true)
                    partFile.delete()
                }
                UpdateState.downloadProgress.value = 1f
                UpdateState.saveApkPath(apkFile)
                return@withContext apkFile
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                lastError = e
                // Keep partFile so the next attempt resumes from where this one stopped.
                val backoff = minOf(MAX_BACKOFF_MS, BASE_BACKOFF_MS shl attempt)
                delay(backoff)
            }
        }
        throw lastError ?: IOException("Не удалось загрузить обновление")
    }

    /** One streaming pass; resumes via Range when a .part already exists. */
    private suspend fun streamWithResume(url: String, partFile: File, expectedSize: Long) {
        val ctx = coroutineContext
        var have = if (partFile.exists()) partFile.length() else 0L
        if (expectedSize in 1 until have) { // stale/oversized part → discard and restart
            partFile.delete()
            have = 0L
        }

        val conn = (URL(url).openConnection() as HttpURLConnection).apply {
            connectTimeout = CONNECT_TIMEOUT_MS
            readTimeout = READ_TIMEOUT_MS
            instanceFollowRedirects = true
            setRequestProperty("User-Agent", HTTPClient.userAgent)
            setRequestProperty("Accept-Encoding", "identity") // no gzip → Content-Length is real
            if (have > 0) setRequestProperty("Range", "bytes=$have-")
        }
        try {
            conn.connect()
            val append: Boolean
            when (conn.responseCode) {
                HttpURLConnection.HTTP_PARTIAL -> append = true            // 206 — server resumed
                HttpURLConnection.HTTP_OK -> { append = false; have = 0L } // 200 — full body, no resume
                else -> throw IOException("HTTP ${conn.responseCode}")
            }
            val thisLen = conn.getHeaderField("Content-Length")?.toLongOrNull() ?: -1L
            val total = when {
                expectedSize > 0 -> expectedSize
                append && thisLen > 0 -> have + thisLen
                thisLen > 0 -> thisLen
                else -> -1L
            }
            conn.inputStream.use { input ->
                FileOutputStream(partFile, append).use { out ->
                    val buf = ByteArray(BUFFER)
                    var done = have
                    while (true) {
                        ctx.ensureActive()
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        done += n
                        UpdateState.downloadProgress.value =
                            if (total > 0) (done.toFloat() / total).coerceIn(0f, 1f) else null
                    }
                    out.flush()
                }
            }
        } finally {
            conn.disconnect()
        }
    }

    /** True only for a complete file we can actually confirm (size and/or sha256). */
    private fun verifies(f: File, size: Long, sha: String): Boolean {
        if (!f.exists() || f.length() == 0L) return false
        if (size > 0 && f.length() != size) return false
        if (sha.isNotBlank()) return sha256Of(f).equals(sha, ignoreCase = true)
        return size > 0 // size matched and no hash published → accept; nothing to confirm → reject
    }

    private fun sha256Of(f: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { ins ->
            val buf = ByteArray(BUFFER)
            while (true) {
                val n = ins.read(buf)
                if (n < 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    override fun close() {}

    private companion object {
        const val MAX_ATTEMPTS = 8
        const val BASE_BACKOFF_MS = 1_000L
        const val MAX_BACKOFF_MS = 30_000L
        const val CONNECT_TIMEOUT_MS = 20_000
        const val READ_TIMEOUT_MS = 60_000
        const val BUFFER = 64 * 1024
    }
}

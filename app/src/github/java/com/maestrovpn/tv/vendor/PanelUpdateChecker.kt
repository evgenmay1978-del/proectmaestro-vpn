package com.maestrovpn.tv.vendor

import com.maestrovpn.tv.BuildConfig
import com.maestrovpn.tv.ktx.unwrap
import com.maestrovpn.tv.update.UpdateInfo
import com.maestrovpn.tv.utils.HTTPClient
import io.nekohasekai.libbox.Libbox
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.Closeable

/**
 * Panel-hosted update channel. The app prefers this over GitHub because the
 * device reaches the panel (BuildConfig.BACKEND_URL = https://wapmixx.ru:8911,
 * polled for /sub every 15 min) from a Russian ISP, but NOT github.com /
 * objects.githubusercontent.com (throttled in RU since 2025 — the same reason
 * we route Russian services direct). Reads a tiny static manifest:
 *
 *   GET <BACKEND_URL>/update/update.json
 *   { "version_code":27, "version_name":"1.0.27",
 *     "apk_url":"/update/MaestroVPN-TV-1.0.27-debug.apk",
 *     "size":143542499, "sha256":"…", "notes":"…" }
 *
 * and returns an UpdateInfo when the advertised version is newer than installed.
 * Returns null (caller falls back to GitHub) on any error.
 */
class PanelUpdateChecker : Closeable {
    companion object {
        private val BASE = BuildConfig.BACKEND_URL.trimEnd('/')
        private val MANIFEST_URL = "$BASE/update/update.json"
    }

    private val client = Libbox.newHTTPClient().apply {
        modernTLS()
        keepAlive()
    }

    private val json = Json { ignoreUnknownKeys = true }

    fun checkUpdate(): UpdateInfo? {
        val request = client.newRequest()
        request.setURL(MANIFEST_URL)
        request.setUserAgent(HTTPClient.userAgent)
        val content = request.execute().content.unwrap
        val m = json.decodeFromString<PanelManifest>(content)
        if (m.versionName.isBlank()) return null
        // A blank apk_url would resolve to "$BASE/" (a directory, not an APK) → reject the manifest.
        if (m.apkUrl.isBlank()) return null
        // Libbox.compareSemver(a, b) == a is newer than b.
        if (!Libbox.compareSemver(m.versionName, BuildConfig.VERSION_NAME)) return null
        val apk = when {
            m.apkUrl.startsWith("http") -> m.apkUrl
            m.apkUrl.startsWith("/") -> BASE + m.apkUrl
            else -> "$BASE/${m.apkUrl}"
        }
        return UpdateInfo(
            versionCode = m.versionCode,
            versionName = m.versionName,
            downloadUrl = apk,
            releaseUrl = apk,
            releaseNotes = m.notes,
            isPrerelease = false,
            fileSize = m.size,
            sha256 = m.sha256,
        )
    }

    override fun close() {
        client.close()
    }

    @Serializable
    data class PanelManifest(
        @SerialName("version_code") val versionCode: Int = 0,
        @SerialName("version_name") val versionName: String = "",
        @SerialName("apk_url") val apkUrl: String = "",
        val size: Long = 0,
        val sha256: String = "",
        val notes: String? = null,
    )
}

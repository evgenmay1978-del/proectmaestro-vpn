package com.maestrovpn.tv.whitelist

import android.content.Intent
import android.net.Network
import android.os.SystemClock
import androidx.preference.PreferenceDataStore
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.constant.SettingsKey
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.preference.OnPreferenceDataStoreChangeListener
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.CopyOnWriteArrayList

internal data class WhiteListMenuPreview(val labels: Map<String, String>, val deadlineMillis: Long)

internal fun whiteListMenuPreview(
    previous: Map<String, String>, sameContext: Boolean, allowed: Boolean, result: WhiteListRuntimeFetch,
): WhiteListMenuPreview = when {
    !allowed -> WhiteListMenuPreview(emptyMap(), 0)
    result is WhiteListRuntimeFetch.Ready -> WhiteListMenuPreview(
        result.runtime.profiles.associate { it.tag to it.label }, result.runtime.deadlineMillis,
    )
    sameContext && result == WhiteListRuntimeFetch.Unavailable -> WhiteListMenuPreview(previous, 0)
    else -> WhiteListMenuPreview(emptyMap(), 0)
}

/** Same-process intent mailbox. No credentials, saved profile changes, or exported IPC. */
internal object WhiteListSelection {
    const val ACTION = "com.maestrovpn.tv.CDN_SELECTION"
    data class Request(val epoch: Long, val profileId: Long, val revision: Long, val tag: String, val requestedAt: Long)
    data class View(val labels: Map<String, String> = emptyMap(), val selected: String? = null, val active: String? = null, val cellular: Boolean = false)
    private val mutableView = MutableStateFlow(View())
    val view = mutableView.asStateFlow()
    private var epoch = 0L
    private var revision = 0L
    private var request: Request? = null
    private var previewDeadline = 0L
    private var previewNetwork: Network? = null
    private var previewAccount: Pair<Long, Long>? = null
    private val invalidations = CopyOnWriteArrayList<() -> Unit>()
    // A non-secret stop marker prevents START_STICKY from choosing ordinary after process death.
    private val preferences = Application.application.getSharedPreferences("cdn-selection", android.content.Context.MODE_PRIVATE)
    fun requiresExplicitChoice(): Boolean = preferences.getBoolean("requires-explicit-choice", false)

    init {
        Settings.dataStore.registerChangeListener(object : OnPreferenceDataStoreChangeListener {
            override fun onPreferenceDataStoreChanged(store: PreferenceDataStore, key: String) {
                if (key == SettingsKey.SELECTED_PROFILE) accountChanged()
            }
        })
        ProfileManager.registerCallback { accountChanged() }
    }

    @Synchronized fun account(): Pair<Long, Long> = Settings.selectedProfile to revision
    @Synchronized fun current(): Request? = request
    @Synchronized fun version(): Long = epoch
    @Synchronized fun matches(value: Request): Boolean = request == value && account() == (value.profileId to value.revision)
    @Synchronized fun label(tag: String): String? = mutableView.value.labels[tag]
    fun addInvalidation(listener: () -> Unit) { invalidations.add(listener) }
    fun removeInvalidation(listener: () -> Unit) { invalidations.remove(listener) }

    private fun accountChanged() {
        synchronized(this) { revision++; clearLocked() }
        invalidations.forEach { it() }
    }

    @Synchronized fun preview(account: Pair<Long, Long>, network: Network?, result: WhiteListRuntimeFetch) {
        if (account != WhiteListSelection.account()) return
        val allowed = WhiteListSession.isCellular(network)
        val preview = whiteListMenuPreview(mutableView.value.labels,
            previewAccount == account && previewNetwork == network, allowed, result)
        previewAccount = account
        previewNetwork = network.takeIf { allowed }
        previewDeadline = preview.deadlineMillis
        val active = mutableView.value.active
        val labels = preview.labels.toMutableMap()
        if (allowed && active != null && result != WhiteListRuntimeFetch.Denied) {
            mutableView.value.labels[active]?.let { labels[active] = it }
        }
        mutableView.value = mutableView.value.copy(labels = labels, cellular = allowed)
    }

    @Synchronized fun select(tag: String, network: Network?): Boolean {
        if (tag.startsWith("cdn:")) {
            if (!WhiteListSession.isCellular(network) || network != previewNetwork || previewAccount != account() ||
                SystemClock.elapsedRealtime() >= previewDeadline || tag !in mutableView.value.labels) return false
        } else if (request == null && mutableView.value.active == null) return false
        val account = account()
        if (!preferences.edit().putBoolean("requires-explicit-choice", tag.startsWith("cdn:")).commit()) return false
        request = Request(++epoch, account.first, account.second, tag, SystemClock.elapsedRealtime())
        mutableView.value = mutableView.value.copy(selected = tag)
        invalidations.forEach { it() }
        Application.application.sendBroadcast(Intent(ACTION).setPackage(Application.application.packageName))
        return true
    }

    /** A network handover revokes CDN consent; returning to cellular never resumes it. */
    @Synchronized fun restoreOrdinary(value: Request, tag: String): Request? {
        if (!matches(value) || !value.tag.startsWith("cdn:") || tag.startsWith("cdn:")) return null
        if (!preferences.edit().putBoolean("requires-explicit-choice", false).commit()) return null
        val restored = Request(++epoch, value.profileId, value.revision, tag, SystemClock.elapsedRealtime())
        request = restored
        previewDeadline = 0
        previewNetwork = null
        previewAccount = null
        mutableView.value = View()
        return restored
    }

    @Synchronized fun started(value: Request): Boolean {
        if (!matches(value)) return false
        val cdn = value.tag.takeIf { it.startsWith("cdn:") }
        mutableView.value = mutableView.value.copy(selected = cdn, active = cdn)
        if (!value.tag.startsWith("cdn:")) request = null
        return true
    }

    @Synchronized fun clear(value: Request? = null, retainLabels: Boolean = true) {
        if (value == null || request == value) {
            preferences.edit().putBoolean("requires-explicit-choice", false).apply()
            clearLocked(retainLabels)
        }
    }
    @Synchronized fun destroyed() { clearLocked(retainLabels = true) }
    private fun clearLocked(retainLabels: Boolean = false) {
        val network = if (retainLabels) WhiteListSession.network() else null
        val allowed = WhiteListSession.isCellular(network)
        // Stopping clears consent and freshness; the same phone context may keep server names.
        val preview = whiteListMenuPreview(mutableView.value.labels,
            retainLabels && previewAccount == account() && previewNetwork == network,
            allowed, WhiteListRuntimeFetch.Unavailable)
        epoch++
        request = null
        previewDeadline = 0
        previewNetwork = network.takeIf { preview.labels.isNotEmpty() }
        previewAccount = account().takeIf { preview.labels.isNotEmpty() }
        mutableView.value = View(labels = preview.labels, cellular = allowed)
    }
}

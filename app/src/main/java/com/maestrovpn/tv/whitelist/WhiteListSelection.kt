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

/** Same-process intent mailbox. No credentials, saved profile changes, or exported IPC. */
internal object WhiteListSelection {
    const val ACTION = "com.maestrovpn.tv.CDN_SELECTION"
    data class Request(val epoch: Long, val profileId: Long, val revision: Long, val tag: String, val requestedAt: Long)
    data class View(val labels: Map<String, String> = emptyMap(), val selected: String? = null, val active: String? = null)
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

    @Synchronized fun preview(account: Pair<Long, Long>, network: Network?, runtime: WhiteListRuntime?) {
        if (account != WhiteListSelection.account()) return
        previewAccount = account
        previewNetwork = network
        previewDeadline = runtime?.deadlineMillis ?: 0L
        val active = mutableView.value.active
        val labels = runtime?.profiles?.associate { it.tag to it.label }.orEmpty().toMutableMap()
        if (active != null) mutableView.value.labels[active]?.let { labels[active] = it }
        mutableView.value = mutableView.value.copy(labels = labels)
    }

    @Synchronized fun select(tag: String, network: Network?): Boolean {
        if (tag.startsWith("cdn:")) {
            if (network == null || network != previewNetwork || previewAccount != account() ||
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

    @Synchronized fun started(value: Request): Boolean {
        if (!matches(value)) return false
        val cdn = value.tag.takeIf { it.startsWith("cdn:") }
        mutableView.value = mutableView.value.copy(selected = cdn, active = cdn)
        if (!value.tag.startsWith("cdn:")) request = null
        return true
    }

    @Synchronized fun clear(value: Request? = null) {
        if (value == null || request == value) {
            preferences.edit().putBoolean("requires-explicit-choice", false).apply()
            clearLocked()
        }
    }
    @Synchronized fun destroyed() { clearLocked() }
    private fun clearLocked() {
        epoch++
        request = null
        previewDeadline = 0
        previewNetwork = null
        previewAccount = null
        mutableView.value = View()
    }
}

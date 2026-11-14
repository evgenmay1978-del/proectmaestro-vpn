package com.maestrovpn.tv.whitelist

import android.content.Context
import android.util.Log
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Lifecycle of the local xhttp runner, driven by which outbound the user has selected.
 *
 * The node list itself lives in the sing-box profile (socks-outbound tags "xhttp:<slug>" emitted by
 * the panel), so the selector and every form factor — phone and TV share one APK and one
 * GroupsViewModel — already see these nodes. All this object does is keep the matching child
 * process alive: start it on selection, stop it on any other selection, restore it after a service
 * restart (the engine reloads its own selected outbound from its cache, so the runner must follow).
 */
internal object SubXhttpSession {
    private const val TAG = "SubXhttpSession"
    private const val PREFS = "subxhttp"
    private const val KEY_ACTIVE = "active_tag"

    /** Group the engine's automatic pick lives in; the safe place to park a failed selection. */
    private const val FALLBACK_TAG = "auto"

    private fun prefs() = Application.application.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** Remembered pick, so a service restart resumes the runner the engine still routes into. */
    fun savedTag(): String? = prefs().getString(KEY_ACTIVE, null)

    /**
     * Called right after libbox accepted a new selection. The process must be up before traffic is
     * routed into it, so the start runs off the main thread (it waits for the SOCKS port).
     *
     * Returns false when the pick could NOT be served. That matters more than it looks: libbox has
     * already accepted the selection by now, so a dead runner leaves the whole tunnel pointing at a
     * closed 127.0.0.1 port — the VPN looks connected and the phone has no internet. The owner hit
     * exactly this on the Turkish node (14.11.2026) and had to switch servers himself. So on failure
     * the selection is rolled back to the automatic group instead of being left dead.
     */
    suspend fun onSelected(groupTag: String?, tag: String?): Boolean = withContext(Dispatchers.IO) {
        if (tag == null || !SubXhttpRuntime.isSubXhttpTag(tag)) {
            if (SubXhttpProcess.runningTag() != null) Log.d(TAG, "stop: selection moved away")
            SubXhttpProcess.stop()
            prefs().edit().remove(KEY_ACTIVE).apply()
            return@withContext true
        }
        val node = SubXhttpRuntime.node(tag)
        val address = node?.let { SubXhttpRuntime.resolve(it) ?: it.address }
        val started = node != null && SubXhttpProcess.available() &&
            address != null && SubXhttpProcess.start(node, address)
        prefs().edit().apply { if (started) putString(KEY_ACTIVE, tag) else remove(KEY_ACTIVE) }.apply()
        SubXhttpProcess.fileLog(
            "onSelected " + tag + " started=" + started + " addr=" + (address ?: "-") +
                " available=" + SubXhttpProcess.available(),
        )
        Log.d(TAG, "onSelected " + tag + " started=" + started + " addr=" + (address ?: "-"))
        if (started) {
            node?.let { SubXhttpRuntime.refreshDelay(it) }
            return@withContext true
        }
        // Never leave the tunnel aimed at a dead port: park the group back on the automatic pick.
        if (groupTag != null) {
            val rolledBack = runCatching {
                CommandTarget.standaloneClient().selectOutbound(groupTag, FALLBACK_TAG)
            }
            SubXhttpProcess.fileLog("rollback " + groupTag + " -> " + FALLBACK_TAG + " ok=" + rolledBack.isSuccess)
            Log.w(TAG, "rollback " + groupTag + " -> " + FALLBACK_TAG + " ok=" + rolledBack.isSuccess)
        }
        false
    }

    /** After the engine (re)started: bring up the runner for the remembered pick, if any. */
    fun restore() {
        val tag = savedTag() ?: return
        if (!SubXhttpRuntime.isSubXhttpTag(tag) || !SubXhttpProcess.available()) return
        val node = SubXhttpRuntime.node(tag) ?: return
        val address = SubXhttpRuntime.resolve(node) ?: node.address
        Thread {
            val started = SubXhttpProcess.start(node, address)
            Log.d(TAG, "restore " + tag + " started=" + started)
            SubXhttpProcess.fileLog("restore " + tag + " started=" + started)
            if (started) SubXhttpRuntime.refreshDelay(node)
        }.apply { isDaemon = true; name = "subxhttp-restore" }.start()
    }

    /**
     * Stop the child but KEEP the remembered pick: the engine reloads its own selected outbound
     * from its cache, so after "turn VPN off, turn it back on" it still routes into our port —
     * forgetting the pick here left that port dead and the tunnel useless (measured 14.11).
     * The pick is dropped only when the user selects a regular node ([onSelected]).
     */
    fun stopAll() {
        val tag = savedTag()
        SubXhttpProcess.stop()
        SubXhttpProcess.fileLog("stopAll (kept " + tag + ")")
    }
}

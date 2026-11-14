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
     * Cheap gate the view model calls BEFORE handing the pick to libbox: a node whose entry does
     * not even answer a TCP handshake must not become the active route at all. Doing this first
     * means no outage window — the previous node simply stays selected.
     */
    suspend fun preflight(tag: String?): Boolean = withContext(Dispatchers.IO) {
        if (tag == null || !SubXhttpRuntime.isSubXhttpTag(tag)) return@withContext true
        val node = SubXhttpRuntime.node(tag) ?: return@withContext true
        val ok = SubXhttpRuntime.probeLatency(node) != null
        if (!ok) SubXhttpProcess.fileLog("preflight " + tag + " = unreachable")
        ok
    }

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
        // Gate on reachability BEFORE handing traffic over: libbox has already accepted the pick,
        // so starting a runner for a node we cannot reach just parks the whole phone on a dead
        // port. Probing first turns a 20 s outage into a sub-second no-op (measured with Turkey).
        val reachable = node != null && SubXhttpRuntime.probeLatency(node) != null
        if (node != null && !reachable) {
            SubXhttpProcess.fileLog("unreachable " + tag + " -> not starting")
            Log.w(TAG, "unreachable " + tag + ", rollback requested")
        }
        var started = reachable && address != null && SubXhttpProcess.start(node, address)
        // A started process is not a working tunnel: the German Akonit node accepted six TCP
        // connections while answering none (14.11.2026), and the owner lost the internet because the
        // engine had already been routed into it. So the node must prove itself with a real request
        // through its own SOCKS port BEFORE libbox is told to use it — this runs while the previous
        // route is still in place, which makes a bad node a no-op instead of an outage.
        if (started && node != null) {
            val verified = SubXhttpProcess.verifyTunnel(node.socksPort)
            SubXhttpProcess.fileLog("onSelected " + tag + " started=true verified=" + verified)
            if (!verified) {
                SubXhttpProcess.stop()
                started = false
            }
        }
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
        // Nothing was switched yet (the caller selects only on true), so the previous route is
        // intact and no rollback is needed here.
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
            if (!started) return@Thread
            // On restore the engine is already routing into this port, so a node that cannot carry
            // traffic has to be parked back on the automatic group instead of being left dead.
            if (!SubXhttpProcess.verifyTunnel(node.socksPort)) {
                SubXhttpProcess.stop()
                SubXhttpProcess.fileLog("restore " + tag + " verify failed -> rollback " + FALLBACK_TAG)
                runCatching { CommandTarget.standaloneClient().selectOutbound("select", FALLBACK_TAG) }
                prefs().edit().remove(KEY_ACTIVE).apply()
                return@Thread
            }
            SubXhttpRuntime.refreshDelay(node)
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

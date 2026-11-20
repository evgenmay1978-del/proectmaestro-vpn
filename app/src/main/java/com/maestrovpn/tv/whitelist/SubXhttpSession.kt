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
     * not even complete a TLS handshake on the phone's path must not become the active route at
     * all. Doing this first means no outage window — the previous node simply stays selected.
     */
    suspend fun preflight(tag: String?): Boolean = withContext(Dispatchers.IO) {
        // "Авто" now includes the local xhttp socks ports, so the all-node runner must be up
        // before libbox starts url-testing that group. A failure is not fatal: auto still has the
        // ordinary nodes and keeps working without xhttp.
        if (tag == FALLBACK_TAG) {
            val started = ensureAll()
            SubXhttpProcess.fileLog("preflight " + FALLBACK_TAG + " allRunner=" + started)
            return@withContext true
        }
        when (val decision = SubXhttpRuntime.decisionFor(tag)) {
            is SubXhttpPlan.Decision.Regular -> true
            is SubXhttpPlan.Decision.Refuse -> {
                // The engine's profile and our manifest disagree on this node: libbox would route
                // the whole phone into a loopback port with no runner behind it (14.11.2026).
                SubXhttpProcess.fileLog("preflight " + tag + " refused: " + decision.reason)
                false
            }
            is SubXhttpPlan.Decision.Serve -> {
                // Start every profile port first: the latency then comes through the real local
                // Xray tunnel instead of a TLS probe to the entry (which is a false negative for
                // several Barev nodes on the phone's path).
                ensureAll()
                val node = tag?.let { SubXhttpRuntime.node(it) }
                val ok = node != null && SubXhttpRuntime.probeLatency(node) != null
                if (!ok) SubXhttpProcess.fileLog("preflight " + tag + " = unreachable")
                ok
            }
        }
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
        if (tag == FALLBACK_TAG) {
            val all = ensureAll()
            SubXhttpProcess.fileLog("onSelected " + FALLBACK_TAG + " allRunner=" + all)
            prefs().edit().putString(KEY_ACTIVE, FALLBACK_TAG).apply()
            return@withContext true
        }
        when (val decision = SubXhttpRuntime.decisionFor(tag)) {
            is SubXhttpPlan.Decision.Regular -> {
                if (SubXhttpProcess.runningTag() != null) Log.d(TAG, "stop: selection moved away")
                SubXhttpProcess.stop()
                prefs().edit().remove(KEY_ACTIVE).apply()
                true
            }
            is SubXhttpPlan.Decision.Refuse -> {
                // Profile and manifest do not agree on this node: starting a runner from half the
                // data would park the phone on a dead port, so keep the previous route and let the
                // caller show the error.
                SubXhttpProcess.fileLog("refused " + tag + " -> not starting: " + decision.reason)
                Log.w(TAG, "refused " + tag + ", rollback requested")
                false
            }
            is SubXhttpPlan.Decision.Serve -> {
                val node = tag?.let { SubXhttpRuntime.node(it) }
                if (node == null) {
                    // Cannot happen: Serve requires a manifest node. Refuse rather than guess.
                    SubXhttpProcess.fileLog("onSelected " + tag + " has no manifest node")
                    return@withContext false
                }
                val address = SubXhttpRuntime.resolve(node) ?: node.address
                // Gate on reachability BEFORE handing traffic over: libbox has already accepted the
                // pick, so starting a runner for a node we cannot reach just parks the whole phone
                // on a dead port. Probing first turns a 20 s outage into a sub-second no-op.
                val reachable = SubXhttpRuntime.probeLatency(node) != null
                if (!reachable) {
                    SubXhttpProcess.fileLog("unreachable " + tag + " -> not starting")
                    Log.w(TAG, "unreachable " + tag + ", rollback requested")
                }
                // The runner listens on the PROFILE port (decision.port): that is the port the
                // engine routes into after the reload, even when the manifest still remembers an
                // older number (the desync that killed the tunnel after a subscription update).
                // One process serves every xhttp node, so auto and a manual pick share the same
                // ports. If the all-node config cannot start, fall back to the single-node runner
                // rather than leaving a working pick unserved.
                val allRunner = reachable && address.isNotEmpty() && ensureAll()
                var started = allRunner || (reachable && address.isNotEmpty() &&
                    SubXhttpProcess.start(node, address, decision.port))
                // A started process is not a working tunnel: the German Akonit node accepted six TCP
                // connections while answering none (14.11.2026), and the owner lost the internet
                // because the engine had already been routed into it. So the node must prove itself
                // with a real request through its own SOCKS port BEFORE libbox is told to use it.
                if (started) {
                    val verified = SubXhttpProcess.verifyTunnel(decision.port)
                    SubXhttpProcess.fileLog(
                        "onSelected " + tag + " started=true verified=" + verified + " port=" + decision.port +
                            " allRunner=" + allRunner,
                    )
                    // Do NOT stop the all-node runner when one pick fails: the previous route may be
                    // auto, whose ports this same runner serves. Stopping it would turn a no-op into
                    // an outage. A fallback single-node runner is safe to stop.
                    if (!verified && !allRunner) {
                        SubXhttpProcess.stop()
                        started = false
                    } else if (!verified) {
                        started = false
                    }
                }
                prefs().edit().apply { if (started) putString(KEY_ACTIVE, tag) else remove(KEY_ACTIVE) }.apply()
                SubXhttpProcess.fileLog(
                    "onSelected " + tag + " started=" + started + " addr=" + (address ?: "-") +
                        " port=" + decision.port + " available=" + SubXhttpProcess.available(),
                )
                Log.d(TAG, "onSelected " + tag + " started=" + started + " addr=" + (address ?: "-") + " port=" + decision.port)
                if (started) {
                    SubXhttpRuntime.refreshDelay(node)
                    return@withContext true
                }
                // Nothing was switched yet (the caller selects only on true), so the previous route
                // is intact and no rollback is needed here.
                false
            }
        }
    }

    /** After the engine (re)started: bring up the runner for the remembered pick, if any. */
    fun restore() {
        if (!SubXhttpProcess.available()) return
        val tag = savedTag()
        if (tag == null) {
            // Fresh install or a regular node picked last time. "Авто" is the default route and
            // now contains the xhttp ports, so keep the all-node runner warm: without it url-test
            // would see closed local ports and auto would quietly fall back to ordinary nodes.
            Thread {
                val started = ensureAll()
                SubXhttpProcess.fileLog("restore default auto allRunner=" + started)
            }.apply { isDaemon = true; name = "subxhttp-restore-default" }.start()
            return
        }
        if (tag == FALLBACK_TAG) {
            Thread {
                val started = ensureAll()
                SubXhttpProcess.fileLog("restore " + FALLBACK_TAG + " allRunner=" + started)
            }.apply { isDaemon = true; name = "subxhttp-restore-auto" }.start()
            return
        }
        when (val decision = SubXhttpRuntime.decisionFor(tag)) {
            is SubXhttpPlan.Decision.Regular -> {
                // The tag is ours no longer: nothing to run and nothing to park.
                prefs().edit().remove(KEY_ACTIVE).apply()
            }
            is SubXhttpPlan.Decision.Refuse -> {
                // The subscription reload moved or dropped this node while it was selected: the
                // engine may still route into the old (now dead) loopback port, so park the pick
                // back on the automatic group instead of leaving the phone offline.
                SubXhttpProcess.stop()
                SubXhttpProcess.fileLog("restore " + tag + " refused (" + decision.reason + ") -> rollback " + FALLBACK_TAG)
                runCatching { CommandTarget.standaloneClient().selectOutbound("select", FALLBACK_TAG) }
                prefs().edit().remove(KEY_ACTIVE).apply()
            }
            is SubXhttpPlan.Decision.Serve -> restoreOn(decision.port, tag)
        }
    }

    private fun restoreOn(port: Int, tag: String) {
        val node = SubXhttpRuntime.node(tag) ?: return
        val address = SubXhttpRuntime.resolve(node) ?: node.address
        Thread {
            val allRunner = ensureAll()
            val started = allRunner || SubXhttpProcess.start(node, address, port)
            Log.d(TAG, "restore " + tag + " started=" + started + " port=" + port + " allRunner=" + allRunner)
            SubXhttpProcess.fileLog(
                "restore " + tag + " started=" + started + " port=" + port + " allRunner=" + allRunner,
            )
            if (!started) return@Thread
            // On restore the engine is already routing into this port, so a node that cannot carry
            // traffic has to be parked back on the automatic group instead of being left dead.
            if (!SubXhttpProcess.verifyTunnel(port)) {
                // The all-node runner also serves "auto"; stopping it here would leave auto without
                // its xhttp ports after the rollback. Only a fallback single-node runner is safe to
                // stop.
                if (!allRunner) SubXhttpProcess.stop()
                SubXhttpProcess.fileLog("restore " + tag + " verify failed -> rollback " + FALLBACK_TAG)
                runCatching { CommandTarget.standaloneClient().selectOutbound("select", FALLBACK_TAG) }
                prefs().edit().remove(KEY_ACTIVE).apply()
                return@Thread
            }
            SubXhttpRuntime.refreshDelay(node)
        }.apply { isDaemon = true; name = "subxhttp-restore" }.start()
    }

    /** One runner process for every xhttp node that the live engine profile knows. */
    private fun ensureAll(): Boolean {
        val ports = SubXhttpRuntime.profilePorts()
        if (ports.isEmpty()) return false
        val nodes = SubXhttpRuntime.nodes().filter { ports.containsKey(it.tag) }
        if (nodes.isEmpty()) return false
        return SubXhttpProcess.startAll(nodes, ports)
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

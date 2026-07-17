package com.maestrovpn.tv.compose.screen.dashboard.groups

import android.util.Log
import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.OutboundGroup
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.bg.WdttManager
import com.maestrovpn.tv.compose.base.BaseViewModel
import com.maestrovpn.tv.compose.base.ScreenEvent
import com.maestrovpn.tv.compose.model.Group
import com.maestrovpn.tv.compose.model.GroupItem
import com.maestrovpn.tv.compose.model.toList
import com.maestrovpn.tv.constant.Status
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.utils.httpGetStringTimed
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.AppLifecycleObserver
import com.maestrovpn.tv.utils.CommandClient
import com.maestrovpn.tv.utils.CommandTarget
import com.maestrovpn.tv.utils.MaestroSub
import com.maestrovpn.tv.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.util.concurrent.atomic.AtomicInteger

// How long a protocol tapped while the VPN was OFF stays "armed". If the VPN comes up within this
// window the pick is applied and cleared; otherwise it's DROPPED so it can never apply to an
// unrelated later start (e.g. after VPN-consent was denied, or a protocol that never appears).
// Generous vs a normal tun bring-up — the command feed connects within a couple of seconds.
// 60s (was 20s): the TTL runs from the TAP, and a first-ever connect pops the system VPN-consent
// dialog — a user reading it slower than the TTL silently lost the protocol pick to «Авто».
private const val PENDING_SELECT_TTL_MS = 60_000L

// olcRTC liveness watchdog: how often to check the child process while olcRTC is the active
// outbound, and how many consecutive failed respawns before giving up + telling the user.
private const val OLC_WATCHDOG_INTERVAL_MS = 8_000L
private const val OLC_WATCHDOG_MAX_FAILS = 3
private const val WDTT_WATCHDOG_INTERVAL_MS = 8_000L
private const val WDTT_WATCHDOG_MAX_FAILS = 3

// WDTT (vk-turn) cold-start self-warm. On a restricted cellular link — e.g. an emergency/drone
// whitelist that permits only VK/OK/Yandex — the WDTT child's ANONYMOUS VK-Calls TURN join is
// captcha-gated until that egress IP has produced trusted VK traffic; that is why users had to
// "open the VK app first" or bootstrap on wifi before switching to cellular. During vk-turn
// selection the app's OWN uid is removed from the tun (WdttVpnPolicy anti-loop bypass), so a
// best-effort HTTPS touch to the same VK/OK hosts the child itself contacts egresses DIRECT with
// SNI=vk.com — the identical signal, applied automatically. Fire-and-forget: the response is
// ignored, it exists only to prime the operator's VK zero-rating/DPI classification + DNS and to
// raise VK anti-abuse reputation for this IP. Does NOT beat a hard persistent captcha wall (that
// needs the child-side fixes) — it removes the common route/reputation warmup.
private val WDTT_WARMUP_URLS = listOf("https://vk.com/", "https://api.vk.me/", "https://calls.okcdn.ru/")
private const val WDTT_WARMUP_TIMEOUT_MS = 4_000L
private const val WDTT_WARMUP_BACKOFF_MS = 1_500L
private const val WDTT_START_ATTEMPTS = 2

data class GroupsUiState(
    val groups: List<Group> = emptyList(),
    val isLoading: Boolean = false,
    val expandedGroups: Set<String> = emptySet(),
    val showCloseConnectionsSnackbar: Boolean = false,
)

sealed class GroupsEvent : ScreenEvent {
    data class GroupSelected(val groupTag: String, val itemTag: String) : GroupsEvent()
}

class GroupsViewModel(private val sharedCommandClient: CommandClient? = null) :
    BaseViewModel<GroupsUiState, GroupsEvent>(),
    CommandClient.Handler {
    private val commandClient: CommandClient
    private val isUsingSharedClient: Boolean

    private val _serviceStatus = MutableStateFlow(Status.Stopped)
    val serviceStatus = _serviceStatus.asStateFlow()
    private var lastServiceStatus: Status = Status.Stopped

    // A protocol the user tapped while the VPN was OFF. We turn the VPN on (caller's job) and apply
    // this pick the moment the live selector arrives — a freshly-built box always defaults the
    // selector to "auto" (no store_selected), so the pick is a real change we must push. STRICTLY
    // bounded: applied+cleared on the first live payload that contains it, cleared on stop, and
    // auto-dropped after PENDING_SELECT_TTL_MS so a stale pick can NEVER switch a by-then-connected
    // user's protocol on its own. See SFANavigation.onSelectProtocol.
    @Volatile
    private var pendingSelect: Pair<String, String>? = null

    // Bumped every time the pending pick changes/clears; the TTL watchdog only drops the pick it
    // was armed for (so a newer tap or an apply can't be clobbered by an older watchdog firing).
    // Atomic — incremented from a couple of call sites; a lost update could strand a watchdog.
    private val pendingGeneration = AtomicInteger(0)

    // olcRTC anti-spam: an olcRTC (re)start blocks up to ~25s. While a select is in flight, ignore
    // further olcRTC taps so a burst can't queue N blocking starts (a device-hang DoS). See selectGroupItem.
    @Volatile private var olcSelectInFlight = false

    // olcRTC liveness watchdog job: while olcRTC is the SELECTED outbound and the tunnel is up, it
    // periodically checks the child process and TRANSPARENTLY respawns it if it died (crash/OOM),
    // instead of silently blackholing traffic through the dead :8808 socks ("подключено, но не работает").
    @Volatile private var olcWatchdog: Job? = null
    @Volatile private var wdttSelectInFlight = false
    @Volatile private var wdttWatchdog: Job? = null

    init {
        if (sharedCommandClient != null) {
            commandClient = sharedCommandClient
            isUsingSharedClient = true
            commandClient.addHandler(this)
        } else {
            commandClient =
                CommandClient(
                    viewModelScope,
                    CommandClient.ConnectionType.Groups,
                    this,
                )
            isUsingSharedClient = false
        }

        viewModelScope.launch {
            combine(
                AppLifecycleObserver.isForeground,
                AppLifecycleObserver.isScreenOn,
                RemoteControlManager.remoteServer,
                RemoteControlManager.isConnected,
                _serviceStatus,
            ) { foreground, screenOn, remoteServer, remoteConnected, status ->
                SessionTarget(
                    // Pause the groups feed while the TV screen is off — mirrors
                    // ConnectionsViewModel; reconnects on SCREEN_ON. VPN data path untouched.
                    connect = foreground && screenOn &&
                        if (remoteServer != null) remoteConnected else status == Status.Started,
                    remoteServerId = remoteServer?.id,
                )
            }.distinctUntilChanged().collect { target ->
                if (target.connect) {
                    if (isUsingSharedClient) {
                        commandClient.addHandler(this@GroupsViewModel)
                    } else {
                        updateState { copy(isLoading = true) }
                        commandClient.connect()
                    }
                } else {
                    if (isUsingSharedClient) {
                        commandClient.removeHandler(this@GroupsViewModel)
                    } else {
                        commandClient.disconnect()
                    }
                }
            }
        }

        // Cold start with the VPN OFF: seed the protocol menu from the saved config right away,
        // so an activated app shows its protocols before any service-status change fires.
        viewModelScope.launch {
            if (_serviceStatus.value != Status.Started && uiState.value.groups.isEmpty()) {
                val offline = loadOfflineGroups()
                if (offline.isNotEmpty()) updateState { copy(groups = offline) }
            }
        }
    }

    private data class SessionTarget(val connect: Boolean, val remoteServerId: Long?)

    override fun createInitialState() = GroupsUiState()

    override fun onCleared() {
        super.onCleared()
        stopOlcWatchdog()
        stopWdttWatchdog()
        if (isUsingSharedClient) {
            commandClient.removeHandler(this)
        } else {
            commandClient.disconnect()
        }
    }

    private fun handleServiceStatusChange(status: Status) {
        if (RemoteControlManager.remoteServer.value != null) {
            return
        }
        if (status != Status.Started) {
            // VPN off: DON'T wipe the protocol list. libbox only serves live groups while running,
            // so fall back to the protocols parsed from the saved sub config — an ACTIVATED app then
            // keeps its protocols visible in BOTH connected and disconnected states (owner request).
            viewModelScope.launch {
                val offline = withPendingSelect(loadOfflineGroups())
                updateState { copy(groups = offline, isLoading = false) }
            }
        }
    }

    /**
     * Protocols parsed from the saved sub config (TypedProfile.path) so the menu can show them while
     * the VPN is OFF. libbox exposes OutboundGroup only while the service runs; this reads the same
     * JSON the service would load and extracts the "select" selector's outbounds. Empty if the app
     * isn't activated (no selected sub profile) or the config can't be read. urlTestDelay=0 (unknown
     * offline). Live libbox groups replace these the moment the service reports Started.
     */
    /** Re-apply an armed pending pick onto an offline-skeleton repaint, so the optimistic chip
     *  highlight survives the Starting-phase repaint instead of flicking back to the default. */
    private fun withPendingSelect(groups: List<Group>): List<Group> {
        val p = pendingSelect ?: return groups
        return groups.map { g ->
            if (g.tag == p.first && g.items.any { it.tag == p.second }) g.copy(selected = p.second) else g
        }
    }

    private suspend fun loadOfflineGroups(): List<Group> = withContext(Dispatchers.IO) {
        runCatching {
            val pid = Settings.selectedProfile
            if (pid == -1L) return@withContext emptyList()
            val profile = ProfileManager.get(pid) ?: return@withContext emptyList()
            val path = profile.typed.path
            if (path.isBlank() || !File(path).isFile) return@withContext emptyList()
            val outbounds = JSONObject(File(path).readText()).optJSONArray("outbounds")
                ?: return@withContext emptyList()
            val typeByTag = HashMap<String, String>()
            var selectTags: List<String> = emptyList()
            var selectDefault = ""
            for (i in 0 until outbounds.length()) {
                val o = outbounds.optJSONObject(i) ?: continue
                val tag = o.optString("tag")
                typeByTag[tag] = o.optString("type")
                if (o.optString("type") == "selector" && tag == "select") {
                    val arr = o.optJSONArray("outbounds")
                    if (arr != null) selectTags = (0 until arr.length()).map { arr.optString(it) }
                    selectDefault = o.optString("default", selectTags.firstOrNull().orEmpty())
                }
            }
            if (selectTags.isEmpty()) return@withContext emptyList()
            listOf(
                Group(
                    tag = "select",
                    type = "selector",
                    selectable = true,
                    selected = selectDefault.ifBlank { selectTags.first() },
                    isExpand = true,
                    items = selectTags.map { GroupItem(it, typeByTag[it].orEmpty(), 0L, 0) },
                ),
            )
        }.getOrDefault(emptyList())
    }

    fun updateServiceStatus(status: Status) {
        if (status == lastServiceStatus) {
            return
        }
        lastServiceStatus = status
        // A real transition into Stopped (a stop, or a start that failed to come up) discards any
        // never-applied pick so it can't ambush a LATER manual start with a stale protocol. This is
        // a genuine transition only — a tap while already Stopped hits the `status == lastServiceStatus`
        // guard above and returns, so the pick set just before the start survives to Started.
        if (status == Status.Stopped) {
            pendingSelect = null
            pendingGeneration.incrementAndGet()
            stopOlcWatchdog() // tunnel down → nothing to guard (BoxService already stopped the child)
            stopWdttWatchdog()
            WdttManager.stop()
        }
        viewModelScope.launch {
            _serviceStatus.emit(status)
            handleServiceStatusChange(status)
        }
    }

    /**
     * The user tapped a protocol chip while the VPN was OFF. Remember it (with an optimistic
     * highlight so the chip lights up during "Подключение…") and let the caller turn the VPN on;
     * [updateGroups] applies it once libbox reports the live selector, spawning olcRTC's child
     * first if that's the pick. Result: tapping any protocol both connects AND lands on exactly
     * that protocol (owner request).
     */
    fun setPendingSelect(groupTag: String, itemTag: String) {
        pendingSelect = groupTag to itemTag
        val gen = pendingGeneration.incrementAndGet()
        updateState {
            copy(groups = groups.map { if (it.tag == groupTag) it.copy(selected = itemTag) else it })
        }
        // Watchdog: if the VPN never comes up (consent denied) or the protocol never shows up in the
        // live selector, drop the pick after the TTL so it can NEVER apply to an unrelated later
        // start. Only clears the pick THIS call armed (generation match) — a newer tap or an apply
        // bumps the generation and takes precedence.
        viewModelScope.launch {
            delay(PENDING_SELECT_TTL_MS)
            if (pendingGeneration.get() == gen) pendingSelect = null
        }
    }

    /**
     * Push a pick recorded while the VPN was off, now that the box is up. Mirrors the olcRTC-aware
     * core of [selectGroupItem] but WITHOUT its "already selected" short-circuit — a fresh box
     * reports "auto", so the pick is always a real change.
     */
    private fun applyPendingSelection(groupTag: String, itemTag: String) {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                // The box may have died between updateGroups and here (OOM/watchdog) — never push a
                // selection at a torn-down command server; the offline-groups path repaints anyway.
                if (_serviceStatus.value != Status.Started) return@launch
                if (itemTag == OlcrtcManager.OUTBOUND_TAG) {
                    if (!OlcrtcManager.ensureStarted()) {
                        sendError(IllegalStateException("olcRTC: видео-туннель не поднялся (см. логи)"))
                        return@launch
                    }
                    WdttManager.stop()
                    stopWdttWatchdog()
                } else if (itemTag == WdttManager.OUTBOUND_TAG) {
                    refreshWdttCreds()
                    if (!ensureWdttStartedWithWarmup()) {
                        sendError(IllegalStateException("VK-туннель не поднялся (см. логи)"))
                        return@launch
                    }
                    OlcrtcManager.stop()
                    stopOlcWatchdog()
                } else {
                    if (OlcrtcManager.isRunning) OlcrtcManager.stop()
                    if (WdttManager.isRunning) WdttManager.stop()
                    stopOlcWatchdog()
                    stopWdttWatchdog()
                }
                CommandTarget.standaloneClient().selectOutbound(groupTag, itemTag)
                if (itemTag == OlcrtcManager.OUTBOUND_TAG) startOlcWatchdog()
                if (itemTag == WdttManager.OUTBOUND_TAG) startWdttWatchdog()
                // Pin the chip to the picked protocol: a live-groups payload racing in right after
                // the box starts reports the momentary "auto", which would otherwise flick the
                // highlight off the protocol the user chose until the next payload catches up.
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(groups = groups.map { if (it.tag == groupTag) it.copy(selected = itemTag) else it })
                    }
                }
            } catch (e: Exception) {
                // Same orphan-reap as selectGroupItem: never leave the spawned child behind.
                if (itemTag == OlcrtcManager.OUTBOUND_TAG) runCatching { OlcrtcManager.stop() }
                if (itemTag == WdttManager.OUTBOUND_TAG) runCatching { WdttManager.stop() }
                sendError(e)
            }
        }
    }

    fun toggleGroupExpand(groupTag: String) {
        val newExpanded = !uiState.value.expandedGroups.contains(groupTag)
        updateState {
            val newExpandedGroups = if (newExpanded) {
                expandedGroups + groupTag
            } else {
                expandedGroups - groupTag
            }
            copy(expandedGroups = newExpandedGroups)
        }
        viewModelScope.launch(Dispatchers.IO) {
            runCatching {
                CommandTarget.standaloneClient().setGroupExpand(groupTag, newExpanded)
            }
        }
    }

    fun toggleAllGroups() {
        val groups = uiState.value.groups
        val allCollapsed = uiState.value.expandedGroups.isEmpty()
        val newExpanded = allCollapsed

        updateState {
            if (allCollapsed) {
                copy(expandedGroups = groups.map { it.tag }.toSet())
            } else {
                copy(expandedGroups = emptySet())
            }
        }

        viewModelScope.launch(Dispatchers.IO) {
            groups.forEach { group ->
                runCatching {
                    CommandTarget.standaloneClient().setGroupExpand(group.tag, newExpanded)
                }
            }
        }
    }

    fun selectGroupItem(groupTag: String, itemTag: String) {
        // Check if this is actually a different selection
        val currentGroup = uiState.value.groups.find { it.tag == groupTag }
        val isOlc = itemTag == OlcrtcManager.OUTBOUND_TAG
        val isWdtt = itemTag == WdttManager.OUTBOUND_TAG
        // Re-tapping the ALREADY-selected item is normally a no-op — EXCEPT olcRTC when its child
        // died: traffic then blackholes through the dead :8808 socks and a re-tap is manual recovery
        // (the watchdog usually recovers it first, but keep this as a fallback).
        val olcDeadRetap = isOlc && !OlcrtcManager.isRunning
        val wdttDeadRetap = isWdtt && !WdttManager.isRunning
        if (currentGroup?.selected == itemTag && !olcDeadRetap && !wdttDeadRetap) {
            return
        }
        // Anti-spam: an olcRTC (re)start blocks up to ~25s; drop taps while one is already running so
        // a frustrated user spamming the chip can't queue N blocking starts (device-hang DoS).
        if (isOlc) {
            if (olcSelectInFlight) return
            olcSelectInFlight = true
        }
        if (isWdtt) {
            if (wdttSelectInFlight) return
            wdttSelectInFlight = true
        }

        // A real manual switch is authoritative — drop any armed "tapped-while-off" pick so a
        // still-pending pick can never re-apply over the protocol the user just chose live.
        pendingSelect = null
        pendingGeneration.incrementAndGet()

        viewModelScope.launch(Dispatchers.IO) {
            try {
                // olcRTC (separate-process WebRTC-disguise fallback): the socks outbound only
                // carries traffic once the child binary is up. Start it BEFORE selecting; abort the
                // switch if it doesn't come up. Switching to anything else tears the child down.
                if (isOlc) {
                    // Pull the freshest room/key first so selecting olcRTC right after a panel/bot
                    // room swap connects to the NEW room (ensureStarted restarts on a change).
                    refreshOlcCreds()
                    if (!OlcrtcManager.ensureStarted()) {
                        sendError(IllegalStateException("olcRTC: видео-туннель не поднялся (см. логи)"))
                        return@launch
                    }
                    WdttManager.stop()
                    stopWdttWatchdog()
                } else if (isWdtt) {
                    refreshWdttCreds()
                    if (!ensureWdttStartedWithWarmup()) {
                        sendError(IllegalStateException("VK-туннель не поднялся (см. логи)"))
                        return@launch
                    }
                    OlcrtcManager.stop()
                    stopOlcWatchdog()
                } else {
                    if (OlcrtcManager.isRunning) OlcrtcManager.stop()
                    if (WdttManager.isRunning) WdttManager.stop()
                    stopOlcWatchdog()
                    stopWdttWatchdog()
                }

                // Select the new outbound immediately
                CommandTarget.standaloneClient().selectOutbound(groupTag, itemTag)
                if (isOlc) startOlcWatchdog() // now guard olcRTC liveness (auto-respawn on child death)
                if (isWdtt) startWdttWatchdog()

                // Update local state and show snackbar
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(
                            groups =
                            groups.map { group ->
                                if (group.tag == groupTag) {
                                    group.copy(selected = itemTag)
                                } else {
                                    group
                                }
                            },
                            showCloseConnectionsSnackbar = true,
                        )
                    }
                    sendEvent(GroupsEvent.GroupSelected(groupTag, itemTag))
                }
            } catch (e: Exception) {
                // The olcRTC child may already be up when the select throws (e.g. the live
                // selector has no `olcrtc` outbound) — reap it, or a fake-video process keeps
                // streaming with nothing routed through it.
                if (isOlc) runCatching { OlcrtcManager.stop() }
                if (isWdtt) runCatching { WdttManager.stop() }
                sendError(e)
            } finally {
                if (isOlc) olcSelectInFlight = false
                if (isWdtt) wdttSelectInFlight = false
            }
        }
    }

    /**
     * Bring the WDTT (vk-turn) child up on a possibly-restricted cellular link WITHOUT the user
     * having to manually warm the VK route (open the VK app / connect on wifi first). Fires
     * [prewarmVkPath] in PARALLEL with the child start so the cold start gets the operator DPI/DNS
     * + VK IP-reputation priming for free (zero added latency — the warm's ~1s TLS handshake lands
     * while the child is still spinning up its worker groups), and retries a small bounded number
     * of times so a transient captcha / slow-net clears itself instead of forcing the user to
     * re-tap. Not a cure for a hard persistent captcha wall (see the child-side fixes) — it removes
     * the common route/reputation warmup. TV never reaches here: WDTT is hard-gated off on TV.
     */
    private suspend fun ensureWdttStartedWithWarmup(): Boolean {
        repeat(WDTT_START_ATTEMPTS) { attempt ->
            val warm = viewModelScope.launch(Dispatchers.IO) { prewarmVkPath() }
            if (WdttManager.ensureStarted()) {
                warm.cancel()
                return true
            }
            warm.join() // let the warm land before the retry so the next attempt benefits from it
            if (attempt < WDTT_START_ATTEMPTS - 1) delay(WDTT_WARMUP_BACKOFF_MS)
        }
        return false
    }

    /**
     * Best-effort direct-egress touch of the VK/OK hosts the WDTT child uses for its anonymous
     * VK-Calls TURN join, to prime the link the same way opening the VK app does. Egresses direct
     * because the app's own uid is excluded from the tun on the vk-turn path; each request is hard
     * time-bounded and its result is deliberately ignored.
     */
    private suspend fun prewarmVkPath() {
        WDTT_WARMUP_URLS.forEach { url ->
            runCatching { httpGetStringTimed(url, WDTT_WARMUP_TIMEOUT_MS) }
        }
    }

    private suspend fun refreshWdttCreds() {
        runCatching {
            val profile = ProfileManager.list().firstOrNull { it.typed.remoteURL.contains("/sub/") } ?: return
            val json = httpGetStringTimed(MaestroSub.endpoint(profile.typed.remoteURL, "info"), 6_000) ?: return
            val wdtt = JSONObject(json).optJSONObject("vk_turn") ?: return
            WdttManager.setCreds(
                wdtt.optString("server"),
                wdtt.optJSONArray("vk_hashes")?.let { a -> (0 until a.length()).map { a.optString(it) } },
                wdtt.optString("password"),
                wdtt.takeIf { it.has("workers") }?.optInt("workers"),
                wdtt.optString("fingerprint"),
                wdtt.optJSONArray("client_ids")?.let { a -> (0 until a.length()).map { a.optString(it) } },
                wdtt.optString("obfs_mode"),
            )
        }
    }

    private fun startWdttWatchdog() {
        wdttWatchdog?.cancel()
        wdttWatchdog = viewModelScope.launch(Dispatchers.IO) {
            var fails = 0
            while (isActive) {
                delay(WDTT_WATCHDOG_INTERVAL_MS)
                val guarding = _serviceStatus.value == Status.Started &&
                    (liveSelectedTag ?: WdttManager.OUTBOUND_TAG) == WdttManager.OUTBOUND_TAG
                if (!guarding) break
                if (WdttManager.isRunning) { fails = 0; continue }
                if (++fails > WDTT_WATCHDOG_MAX_FAILS) {
                    WdttManager.stop()
                    sendError(IllegalStateException("VK-туннель упал и не восстанавливается — переключитесь на другой протокол"))
                    break
                }
                Log.w("GroupsVM", "WDTT child died while selected — auto-respawning (attempt $fails)")
                // Warm the VK route before respawning too: a child that died mid-session on the
                // restricted link needs the same operator/VK priming to come back.
                viewModelScope.launch(Dispatchers.IO) { prewarmVkPath() }
                WdttManager.ensureStarted()
                if (!isActive || _serviceStatus.value != Status.Started || liveSelectedTag != WdttManager.OUTBOUND_TAG) {
                    WdttManager.stop()
                    break
                }
            }
        }
    }

    private fun stopWdttWatchdog() {
        wdttWatchdog?.cancel()
        wdttWatchdog = null
    }

    /**
     * While olcRTC is the active outbound, poll its child process; if it died (crash/OOM) transparently
     * respawn it instead of leaving the user "connected" over a dead :8808 socks. Gives up after
     * [OLC_WATCHDOG_MAX_FAILS] consecutive failed respawns and surfaces an error. Cancelled on
     * switch-away / stop / VM clear. Respawns coalesce with user taps via OlcrtcManager's own guard.
     */
    /**
     * Re-pull the owner-gated olcRTC params from GET /sub/<tok>/info and push them into
     * [OlcrtcManager]. Lets a room/key SWAP (from the panel or the bot) reach a device that's
     * already connected, WITHOUT a reconnect. A failed/timed-out fetch keeps the cached creds
     * (never revokes) — so it stays safe behind an ISP whitelist that blocks the panel.
     */
    private suspend fun refreshOlcCreds() {
        runCatching {
            val profile = ProfileManager.list().firstOrNull { it.typed.remoteURL.contains("/sub/") } ?: return
            val url = MaestroSub.endpoint(profile.typed.remoteURL, "info")
            val json = httpGetStringTimed(url, 6_000) ?: return // short timeout; keep cached creds on failure
            val olc = JSONObject(json).optJSONObject("olcrtc")
            OlcrtcManager.setCreds(
                provider = olc?.optString("provider"),
                room = olc?.optString("room"),
                key = olc?.optString("key"),
                transport = olc?.optString("transport"),
            )
        }
    }

    // Selector tag from the most recent LIVE groups payload (offline skeleton repaints never
    // touch it): the watchdog's guard used to read the UI mirror, which the screen-off repaint
    // resets to the config default — silently killing the watchdog while olcRTC kept routing.
    @Volatile private var liveSelectedTag: String? = null

    private fun startOlcWatchdog() {
        olcWatchdog?.cancel()
        olcWatchdog = viewModelScope.launch(Dispatchers.IO) {
            // true while olcRTC is STILL the active outbound and the tunnel is up — i.e. we should
            // keep guarding. Also false once the job is cancelled (switch-away / stop / VM clear).
            fun stillGuarding() = isActive && _serviceStatus.value == Status.Started &&
                (liveSelectedTag ?: OlcrtcManager.OUTBOUND_TAG) == OlcrtcManager.OUTBOUND_TAG
            var fails = 0
            var ticks = 0
            while (isActive) {
                delay(OLC_WATCHDOG_INTERVAL_MS)
                if (!stillGuarding()) break
                // Every ~4 ticks (~32s) re-pull /info so a room/key swap from the panel/bot reaches
                // this device without a reconnect. Cheap; skipped-on-failure keeps cached creds.
                if (ticks++ % 4 == 0) refreshOlcCreds()
                if (OlcrtcManager.isRunning) {
                    // Room/key changed under the running child → restart it onto the NEW room, else
                    // it stays joined to the old (now srv-less) room and traffic quietly blackholes.
                    if (OlcrtcManager.credsChanged()) {
                        Log.i("GroupsVM", "olcRTC room/key changed — restarting child onto the new room")
                        OlcrtcManager.ensureStarted() // blocks up to ~25s while it rejoins
                        // The blocking restart may have raced a switch-away — don't orphan the child.
                        if (!stillGuarding()) { OlcrtcManager.stop(); break }
                    }
                    fails = 0; continue
                }
                // Child died while olcRTC is the active outbound → traffic is blackholing. Respawn,
                // but COUNT every death (not just failed ensureStarted): a child that starts then dies
                // within the cycle must not respawn every 8s forever (battery/CPU drain). Give up after
                // MAX consecutive deaths; a respawn that STAYS alive resets the counter (isRunning above).
                fails++
                if (fails > OLC_WATCHDOG_MAX_FAILS) {
                    OlcrtcManager.stop()
                    sendError(IllegalStateException("olcRTC туннель упал и не восстанавливается — переключитесь на другой протокол"))
                    break
                }
                Log.w("GroupsVM", "olcRTC child died while selected — auto-respawning (attempt $fails)")
                OlcrtcManager.ensureStarted() // blocks up to ~25s; success/failure both re-checked next cycle
                // The blocking respawn may have raced with a switch-away / stop / cancel — if we're no
                // longer guarding olcRTC, tear down whatever we just started so it can't orphan on :8808.
                if (!stillGuarding()) {
                    OlcrtcManager.stop()
                    break
                }
            }
        }
    }

    private fun stopOlcWatchdog() {
        olcWatchdog?.cancel()
        olcWatchdog = null
    }

    fun closeConnections() {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                CommandTarget.standaloneClient().closeConnections()
                withContext(Dispatchers.Main) {
                    dismissCloseConnectionsSnackbar()
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    dismissCloseConnectionsSnackbar()
                }
                sendError(e)
            }
        }
    }

    fun dismissCloseConnectionsSnackbar() {
        updateState {
            copy(showCloseConnectionsSnackbar = false)
        }
    }

    fun urlTest(groupTag: String) {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                CommandTarget.standaloneClient().urlTest(groupTag)
            } catch (e: Exception) {
                sendError(e)
            }
        }
    }

    // CommandClient.Handler implementation
    override fun onConnected() {
        viewModelScope.launch(Dispatchers.Main) {
            // Connection established, waiting for groups
        }
    }

    override fun onDisconnected() {
        // Command feed dropped (VPN off / screen off): keep the menu populated from the saved
        // config instead of blanking it, so protocols stay visible while disconnected.
        viewModelScope.launch {
            val offline = withPendingSelect(loadOfflineGroups())
            updateState { copy(groups = offline, isLoading = false) }
        }
    }

    override fun updateGroups(newGroups: MutableList<OutboundGroup>) {
        viewModelScope.launch(Dispatchers.Default) {
            val currentGroups = uiState.value.groups
            val newGroupsMap = newGroups.associateBy { it.tag }

            // Smart merge: preserve existing Group objects when only delays change
            val mergedGroups =
                if (currentGroups.isEmpty()) {
                    // Initial load
                    newGroups.map(::Group)
                } else {
                    currentGroups.map { existingGroup ->
                        val newGroupData = newGroupsMap[existingGroup.tag]
                        if (newGroupData != null) {
                            // Check if only delays have changed
                            val newItems = newGroupData.items.toList()
                            val hasStructuralChange =
                                existingGroup.items.size != newItems.size ||
                                    existingGroup.selected != newGroupData.selected ||
                                    existingGroup.type != newGroupData.type ||
                                    existingGroup.selectable != newGroupData.selectable

                            if (hasStructuralChange) {
                                // Structural change, create new Group
                                Group(newGroupData)
                            } else {
                                // Only delays might have changed, update items efficiently
                                val updatedItems =
                                    existingGroup.items.mapIndexed { index, item ->
                                        val newItemData = newItems.getOrNull(index)
                                        if (newItemData != null &&
                                            item.tag == newItemData.tag &&
                                            item.type == newItemData.type
                                        ) {
                                            // Only update if delay actually changed
                                            if (item.urlTestDelay != newItemData.urlTestDelay ||
                                                item.urlTestTime != newItemData.urlTestTime
                                            ) {
                                                GroupItem(newItemData)
                                            } else {
                                                item // Keep existing object
                                            }
                                        } else {
                                            if (newItemData != null) {
                                                GroupItem(newItemData)
                                            } else {
                                                item // Keep existing if index out of bounds
                                            }
                                        }
                                    }
                                existingGroup.copy(items = updatedItems)
                            }
                        } else {
                            existingGroup
                        }
                    } +
                        newGroups.filter { newGroup ->
                            currentGroups.none { it.tag == newGroup.tag }
                        }.map(::Group)
                }

            // A protocol tapped while the VPN was off applies now that the live selector is here —
            // but ONLY once the tapped item actually exists in the fresh groups. If it's not here
            // yet (groups can arrive in stages) we DON'T discard the pick: a later payload applies
            // it, and the TTL watchdog drops it if it never shows. Pre-set its `selected` in the
            // committed state so the chip doesn't flicker auto→target during connect.
            val pending = pendingSelect
            val pendingOk = pending != null &&
                mergedGroups.any { g -> g.tag == pending.first && g.items.any { it.tag == pending.second } }
            val committedGroups = if (pendingOk) {
                mergedGroups.map { g -> if (g.tag == pending!!.first) g.copy(selected = pending.second) else g }
            } else {
                mergedGroups
            }

            // Live-feed truth for the olcRTC watchdog guard (includes the pending overlay: a
            // pick in flight counts as intent to stay on it).
            liveSelectedTag = committedGroups.firstOrNull { it.tag == "select" }?.selected

            withContext(Dispatchers.Main) {
                updateState {
                    val initialExpandedGroups = if (expandedGroups.isEmpty() && currentGroups.isEmpty()) {
                        committedGroups.filter { it.isExpand }.map { it.tag }.toSet()
                    } else {
                        expandedGroups
                    }
                    copy(
                        groups = committedGroups,
                        expandedGroups = initialExpandedGroups,
                        isLoading = false,
                    )
                }
                if (pendingOk) {
                    pendingSelect = null
                    pendingGeneration.incrementAndGet() // consumed — invalidate its TTL watchdog
                    applyPendingSelection(pending!!.first, pending.second)
                }
            }
        }
    }
}

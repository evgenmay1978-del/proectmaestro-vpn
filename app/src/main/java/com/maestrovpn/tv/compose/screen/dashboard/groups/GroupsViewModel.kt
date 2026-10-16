package com.maestrovpn.tv.compose.screen.dashboard.groups

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.OutboundGroup
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.utils.DeviceFormFactor
import com.maestrovpn.tv.whitelist.WhiteListRuntimeClient
import com.maestrovpn.tv.whitelist.WhiteListSelection
import com.maestrovpn.tv.whitelist.WhiteListSession
import com.maestrovpn.tv.compose.base.BaseViewModel
import com.maestrovpn.tv.compose.base.ScreenEvent
import com.maestrovpn.tv.compose.model.Group
import com.maestrovpn.tv.compose.model.GroupItem
import com.maestrovpn.tv.compose.model.isProtocolSelectionAllowed
import com.maestrovpn.tv.compose.model.toList
import com.maestrovpn.tv.constant.Status
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.AppLifecycleObserver
import com.maestrovpn.tv.utils.CommandClient
import com.maestrovpn.tv.utils.CommandTarget
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

data class GroupsUiState(
    val groups: List<Group> = emptyList(),
    val isLoading: Boolean = false,
    val expandedGroups: Set<String> = emptySet(),
    val showCloseConnectionsSnackbar: Boolean = false,
    val cdnOptions: List<String> = emptyList(),
    val cdnSelected: String? = null,
    val cdnActive: String? = null,
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

    init {
        if (!DeviceFormFactor.isTelevision(Application.application)) {
            viewModelScope.launch {
                combine(WhiteListSelection.view, RemoteControlManager.remoteServer) { value, remote ->
                    value to (remote == null)
                }.collect { (value, local) ->
                    updateState { copy(cdnOptions = if (local) value.labels.keys.toList() else emptyList(),
                        cdnSelected = value.selected.takeIf { local }, cdnActive = value.active.takeIf { local }) }
                }
            }
            // Menu projection only. The existing VPN service performs its own authenticated fetch.
            viewModelScope.launch(Dispatchers.IO) {
                while (isActive) {
                    val account = WhiteListSelection.account()
                    val network = WhiteListSession.network()
                    val visible = AppLifecycleObserver.isForeground.value && AppLifecycleObserver.isScreenOn.value &&
                        RemoteControlManager.remoteServer.value == null
                    val runtime = if (visible && network != null && account.first >= 0) runCatching {
                        val url = ProfileManager.get(account.first)?.typed?.remoteURL
                        if (url != null) WhiteListRuntimeClient.fetch(url, network) else null
                    }.getOrNull() else null
                    WhiteListSelection.preview(account, network, runtime)
                    delay(1_000)
                }
            }
        }
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
        if (!isProtocolSelectionAllowed(p.first, p.second)) return groups
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
        }
        viewModelScope.launch {
            _serviceStatus.emit(status)
            handleServiceStatusChange(status)
        }
    }

    /**
     * The user tapped a protocol chip while the VPN was OFF. Remember it (with an optimistic
     * highlight so the chip lights up during "Подключение…") and let the caller turn the VPN on;
     * [updateGroups] applies it once libbox reports the live selector. Result: tapping any
     * protocol both connects AND lands on exactly that protocol (owner request).
     */
    fun setPendingSelect(groupTag: String, itemTag: String) {
        if (!isProtocolSelectionAllowed(groupTag, itemTag)) return
        if (selectCdnOrRestore(groupTag, itemTag)) return
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
     * Push a pick recorded while the VPN was off, now that the box is up. Mirrors the
     * core of [selectGroupItem] but WITHOUT its "already selected" short-circuit — a fresh box
     * reports "auto", so the pick is always a real change.
     */
    private fun applyPendingSelection(groupTag: String, itemTag: String) {
        if (!isProtocolSelectionAllowed(groupTag, itemTag)) return
        viewModelScope.launch(Dispatchers.IO) {
            try {
                // The box may have died between updateGroups and here (OOM/watchdog) — never push a
                // selection at a torn-down command server; the offline-groups path repaints anyway.
                if (_serviceStatus.value != Status.Started) return@launch
                CommandTarget.standaloneClient().selectOutbound(groupTag, itemTag)
                // Pin the chip to the picked protocol: a live-groups payload racing in right after
                // the box starts reports the momentary "auto", which would otherwise flick the
                // highlight off the protocol the user chose until the next payload catches up.
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(groups = groups.map { if (it.tag == groupTag) it.copy(selected = itemTag) else it })
                    }
                }
            } catch (e: Exception) {
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

    private var manualCdnRefresh: Job? = null
    fun refreshCdnMenu() {
        if (manualCdnRefresh?.isActive == true || RemoteControlManager.remoteServer.value != null ||
            DeviceFormFactor.isTelevision(Application.application)) return
        manualCdnRefresh = viewModelScope.launch(Dispatchers.IO) {
            val account = WhiteListSelection.account()
            val network = WhiteListSession.network()
            val runtime = if (network != null && account.first >= 0) runCatching {
                ProfileManager.get(account.first)?.typed?.remoteURL?.let { WhiteListRuntimeClient.fetch(it, network) }
            }.getOrNull() else null
            WhiteListSelection.preview(account, network, runtime)
            if (_serviceStatus.value == Status.Stopped) {
                val offline = loadOfflineGroups()
                updateState { copy(groups = offline) }
            }
        }
    }

    fun selectCdn(itemTag: String): Boolean {
        if (!itemTag.startsWith("cdn:") || RemoteControlManager.remoteServer.value != null ||
            DeviceFormFactor.isTelevision(Application.application)) return false
        pendingSelect = null
        pendingGeneration.incrementAndGet()
        val network = WhiteListSession.network()
        return WhiteListSelection.select(itemTag, network).also { accepted ->
            if (!accepted) sendError(IllegalStateException(
                if (network == null) "CDN доступен только через мобильную сеть" else "CDN: обновите список подключений",
            ))
        }
    }

    /** CDN requests never reach libbox's direct selector or the deferred transport managers. */
    private fun selectCdnOrRestore(groupTag: String, itemTag: String): Boolean {
        val cdn = itemTag.startsWith("cdn:")
        if (cdn) { selectCdn(itemTag); return true }
        if (RemoteControlManager.remoteServer.value != null || DeviceFormFactor.isTelevision(Application.application)) return cdn
        val managed = WhiteListSelection.current() != null || WhiteListSelection.view.value.active != null
        if (!cdn && !managed) return false
        if (groupTag != "select") return true
        pendingSelect = null
        pendingGeneration.incrementAndGet()
        if (!WhiteListSelection.select(itemTag, WhiteListSession.network())) {
            sendError(IllegalStateException("CDN: обновите список подключений"))
        }
        return true
    }

    fun selectGroupItem(groupTag: String, itemTag: String) {
        if (!isProtocolSelectionAllowed(groupTag, itemTag)) return
        if (selectCdnOrRestore(groupTag, itemTag)) return
        // Check if this is actually a different selection
        val currentGroup = uiState.value.groups.find { it.tag == groupTag }
        if (currentGroup?.selected == itemTag) {
            return
        }

        // A real manual switch is authoritative — drop any armed "tapped-while-off" pick so a
        // still-pending pick can never re-apply over the protocol the user just chose live.
        pendingSelect = null
        pendingGeneration.incrementAndGet()

        viewModelScope.launch(Dispatchers.IO) {
            try {
                // Select the new outbound immediately
                CommandTarget.standaloneClient().selectOutbound(groupTag, itemTag)

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
                sendError(e)
            }
        }
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

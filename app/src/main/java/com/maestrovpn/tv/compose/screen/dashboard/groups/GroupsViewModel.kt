package com.maestrovpn.tv.compose.screen.dashboard.groups

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.OutboundGroup
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.compose.base.BaseViewModel
import com.maestrovpn.tv.compose.base.ScreenEvent
import com.maestrovpn.tv.compose.model.Group
import com.maestrovpn.tv.compose.model.GroupItem
import com.maestrovpn.tv.compose.model.toList
import com.maestrovpn.tv.constant.Status
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.utils.AppLifecycleObserver
import com.maestrovpn.tv.utils.CommandClient
import com.maestrovpn.tv.utils.CommandTarget
import com.maestrovpn.tv.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

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
                val offline = loadOfflineGroups()
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
        viewModelScope.launch {
            _serviceStatus.emit(status)
            handleServiceStatusChange(status)
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
        // Re-tapping the ALREADY-selected item is normally a no-op — EXCEPT olcRTC when its child
        // died (flaky video tunnel): traffic is then blackholing through the dead :8808 socks
        // outbound and re-tapping is the user's natural recovery, so let it through to respawn.
        val olcDeadRetap = itemTag == OlcrtcManager.OUTBOUND_TAG && !OlcrtcManager.isRunning
        if (currentGroup?.selected == itemTag && !olcDeadRetap) {
            return
        }

        viewModelScope.launch(Dispatchers.IO) {
            try {
                // olcRTC (separate-process WebRTC-disguise fallback): the socks outbound only
                // carries traffic once the child binary is up. Start it BEFORE selecting; abort the
                // switch if it doesn't come up. Switching to anything else tears the child down.
                if (itemTag == OlcrtcManager.OUTBOUND_TAG) {
                    if (!OlcrtcManager.ensureStarted()) {
                        sendError(IllegalStateException("olcRTC: видео-туннель не поднялся (см. логи)"))
                        return@launch
                    }
                } else if (OlcrtcManager.isRunning) {
                    OlcrtcManager.stop()
                }

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
            val offline = loadOfflineGroups()
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

            withContext(Dispatchers.Main) {
                updateState {
                    val initialExpandedGroups = if (expandedGroups.isEmpty() && currentGroups.isEmpty()) {
                        mergedGroups.filter { it.isExpand }.map { it.tag }.toSet()
                    } else {
                        expandedGroups
                    }
                    copy(
                        groups = mergedGroups,
                        expandedGroups = initialExpandedGroups,
                        isLoading = false,
                    )
                }
            }
        }
    }
}

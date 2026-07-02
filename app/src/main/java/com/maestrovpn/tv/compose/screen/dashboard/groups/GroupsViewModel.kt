package com.maestrovpn.tv.compose.screen.dashboard.groups

import android.widget.Toast
import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.OutboundGroup
import com.maestrovpn.tv.Application
import com.maestrovpn.tv.bg.OlcrtcManager
import com.maestrovpn.tv.compose.base.BaseViewModel
import com.maestrovpn.tv.compose.base.ScreenEvent
import com.maestrovpn.tv.compose.model.Group
import com.maestrovpn.tv.compose.model.GroupItem
import com.maestrovpn.tv.compose.model.toList
import com.maestrovpn.tv.constant.Status
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
            updateState {
                copy(
                    groups = emptyList(),
                    isLoading = false,
                )
            }
        }
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
                    // The SOCKS listener only comes up after the WebRTC video tunnel is fully
                    // established — up to ~90s while a TURN-over-TCP relay warms up in RU whitelist
                    // regions. Surface that so a long wait doesn't read as a freeze.
                    withContext(Dispatchers.Main) {
                        Toast.makeText(
                            Application.application,
                            "Запуск видео-туннеля, это может занять до 1,5 минут…",
                            Toast.LENGTH_LONG,
                        ).show()
                    }
                    if (!OlcrtcManager.ensureStarted()) {
                        sendError(IllegalStateException("olcRTC: видео-туннель не поднялся за 90 сек — подождите и попробуйте ещё раз (см. логи)"))
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
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                copy(
                    groups = emptyList(),
                    isLoading = false,
                )
            }
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

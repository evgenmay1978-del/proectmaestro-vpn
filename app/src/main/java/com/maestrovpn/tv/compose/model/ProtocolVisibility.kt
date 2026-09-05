package com.maestrovpn.tv.compose.model

import java.util.Locale

// Presentation/manual-selection policy only; persisted profiles and transport runtime stay intact.
private val deferredTransportAliases = setOf("vk-turn", "wdtt", "olcrtc", "olc rtc", "webrtc")

internal fun isProtocolVisibleInUi(tag: String): Boolean =
    tag.trim().lowercase(Locale.ROOT) !in deferredTransportAliases

internal fun visibleProtocolTags(tags: List<String>): List<String> = tags.filter(::isProtocolVisibleInUi)

internal fun isProtocolSelectionAllowed(groupTag: String, itemTag: String): Boolean =
    isProtocolVisibleInUi(groupTag) && isProtocolVisibleInUi(itemTag)

/** An actual hidden selection must not be presented as a different, visible active protocol. */
internal fun visibleActiveProtocol(activeProtocol: String?, selected: String?): String? =
    (activeProtocol?.takeIf { it.isNotBlank() } ?: selected)
        ?.takeIf { it.isNotBlank() && isProtocolVisibleInUi(it) }

/** UI projection shared by local/remote Groups screens and status sheets; never mutate live groups. */
internal fun visibleProtocolGroups(groups: List<Group>): List<Group> = groups.mapNotNull { group ->
    if (!isProtocolVisibleInUi(group.tag)) return@mapNotNull null
    val items = group.items.filter { isProtocolVisibleInUi(it.tag) }
    if (items.isEmpty() && group.items.isNotEmpty()) return@mapNotNull null
    group.copy(selected = visibleActiveProtocol(null, group.selected).orEmpty(), items = items)
}

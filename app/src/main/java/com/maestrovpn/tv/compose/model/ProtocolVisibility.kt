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

// --- External JFL subscription (owner-supplied, delivered to every client via the sub shim). ---
// Servers arrive as tags like "JFL · 🇩🇪 Германия". Both homes fold that list into ONE tile
// ("JFL") and the tile opens the country picker, so the TV grid stays 8 tiles (owner, 03.11.2026).
internal const val JFL_GROUP_TAG = "jfl"
private const val JFL_TAG_PREFIX = "JFL · "

internal fun isJflServerTag(tag: String?): Boolean =
    tag != null && tag.trim().startsWith(JFL_TAG_PREFIX)

internal fun jflServerCount(tags: List<String>): Int = tags.count(::isJflServerTag)

internal fun jflServerTags(tags: List<String>): List<String> = visibleProtocolTags(tags).filter(::isJflServerTag)

/**
 * What the home grid shows: the visible profile tags without CDN and without the raw JFL country
 * tags, plus a single "jfl" tile when the subscription carries any JFL server. CDN stays off the
 * TV home on purpose (owner: «CDN на тв версии быть не должно»).
 */
internal fun tvHomeProtocolTags(tags: List<String>): List<String> {
    val visible = visibleProtocolTags(tags).filterNot { it.startsWith("cdn:") || isJflServerTag(it) }
    return if (jflServerCount(tags) > 0) visible + JFL_GROUP_TAG else visible
}

/** "42 мс"; null while the server has not been URL-tested yet (delay == 0). */
internal fun pingLabel(delayMs: Int?): String? = delayMs?.takeIf { it > 0 }?.let { "$it мс" }


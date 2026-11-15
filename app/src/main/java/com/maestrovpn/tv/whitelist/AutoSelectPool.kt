package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.util.Locale

/**
 * MaestroVPN's own servers (owner's canon 15.11.2026: «У нас всего два сервера… голландия и
 * испания» — S1 🇪🇸 and S3 🇳🇱; everything else in the subscription comes from third-party
 * subscriptions).
 *
 * An outbound is ours when it dials one of our hosts or carries one of the names the panel and the
 * S1 shim give our nodes. The address is the primary proof (the shim renames tags, the IP of a node
 * is what makes it ours); the names cover a node whose address changed (S1 moved on 06.11.2026).
 * Everything else — JFL, WL/Akonit, the xhttp runners, any provider added later — is third-party.
 */
internal object OwnServers {
    /** S1 ES, S3 NL (both addresses), the retired Italy node, the Yandex relay in front of S1. */
    private val HOSTS = setOf(
        "185.121.12.47",
        "46.30.41.191",
        "46.30.42.151",
        "94.177.106.114",
        "46.21.244.130",
    )

    /** wapmixx.ru (panel, CDN resource) and the old hy2 name of the Italy node. */
    private val DOMAINS = listOf("wapmixx.ru", "wapmix.duckdns.org")

    /** Short tags of the panel's own protocols and of transports that only exist on our nodes. */
    private val SHORT_TAGS = setOf(
        "vless", "vless-s2", "vless-s3", "vless-s4", "hysteria2", "naive", "naiveproxy", "anytls",
        "awg", "olcrtc", "olc rtc", "vk-turn", "wdtt",
    )

    /** Tags the shim puts on third-party nodes; checked before the own-name patterns. */
    private val THIRD_PARTY_PREFIXES = listOf("JFL · ", "WL · ", SubXhttpRuntime.TAG_PREFIX)

    /**
     * Names the S1 shim gives our nodes (TAG_RELABEL, xray_name_configs): «🇪🇸 Испания · VLESS»,
     * «🇳🇱 Нидерланды · VLESS», «🇮🇹 Италия · AnyTLS», «🇪🇸 Испания · S1».
     */
    private val RELABELED = Regex(
        "^(?:\\S+\\s+)?(?:Испания|Нидерланды|Италия|Чехия)\\s*·\\s*" +
            "(?:VLESS|Hysteria2|Naive|NaiveProxy|AnyTLS|AmneziaWG|S\\d)\\b.*",
        RegexOption.IGNORE_CASE,
    )

    fun isOwnHost(host: String?): Boolean {
        val normalized = host?.trim()?.trim('[', ']')?.trimEnd('.')?.lowercase(Locale.ROOT)
        if (normalized.isNullOrEmpty()) return false
        return normalized in HOSTS || DOMAINS.any { normalized == it || normalized.endsWith(".$it") }
    }

    fun isOwnTag(tag: String?): Boolean {
        val text = tag?.trim().orEmpty()
        if (text.isEmpty()) return false
        val lower = text.lowercase(Locale.ROOT)
        // CDN routes ride our own ingress (Yandex CDN → S1); the panel labels them «БС/Yandex …».
        if (lower.startsWith("cdn:") || text.startsWith("БС/")) return true
        if (THIRD_PARTY_PREFIXES.any { text.startsWith(it) }) return false
        if (lower in SHORT_TAGS || "maestrovpn" in lower) return true
        return RELABELED.matches(text)
    }

    /** Leaf outbound or endpoint (awg/wireguard: the server lives in `peers[].address`). */
    fun isOwnOutbound(outbound: JsonObject): Boolean {
        if (isOwnTag(outbound.text("tag")) || isOwnHost(outbound.text("server"))) return true
        val peers = outbound["peers"] as? JsonArray ?: return false
        return peers.any { isOwnHost((it as? JsonObject)?.text("address")) }
    }
}

/**
 * «Авто» picks among third-party servers only: our own servers are taken out of every urltest
 * group of the config the engine is about to load. Every engine start and reload (manual pick,
 * CDN, subscription refresh, UpdateProfileWork, settings) goes through
 * BoxService.applySelectedContent, which is the only caller of startOrReloadService — so no other
 * path can hand the engine an urltest with our servers in it.
 *
 * Our servers stay in the manual list («select»): picking them by hand is unchanged.
 *
 * A group with no third-party member at all is left as it is: an empty urltest is rejected by the
 * engine and the client would lose the VPN (owner's rule: clients must not suffer). The caller logs
 * such a group so the case is visible.
 */
internal object AutoSelectPool {
    data class Result(
        val config: String,
        /** urltest tag → our servers taken out of it. */
        val removed: Map<String, List<String>>,
        /** urltest groups kept as they were because they hold nothing but our servers. */
        val ownOnly: List<String>,
    )

    private val GROUP_TYPES = setOf("selector", "urltest")

    fun exclude(config: String): Result {
        val untouched = Result(config, emptyMap(), emptyList())
        val root = runCatching { Json.parseToJsonElement(config) as? JsonObject }.getOrNull() ?: return untouched
        val outbounds = root["outbounds"] as? JsonArray ?: return untouched
        val endpoints = root["endpoints"] as? JsonArray ?: JsonArray(emptyList())
        val byTag = HashMap<String, JsonObject>()
        for (element in outbounds + endpoints) {
            val outbound = element as? JsonObject ?: continue
            val tag = outbound.text("tag") ?: continue
            // Not putIfAbsent: that is API 24, the app still runs on Android 6 (minSdk 23) boxes.
            if (tag !in byTag) byTag[tag] = outbound
        }
        val verdicts = HashMap<String, Boolean>()
        // A nested group is ours only when every leaf under it is ours: a mixed selector stays,
        // because a manual group cannot be trimmed without changing the user's manual choice.
        fun isOwn(tag: String, path: Set<String>): Boolean {
            verdicts[tag]?.let { return it }
            if (tag in path) return false
            val outbound = byTag[tag]
            val verdict = when {
                outbound == null -> OwnServers.isOwnTag(tag)
                outbound.text("type") in GROUP_TYPES -> {
                    val members = outbound.members()
                    members.isNotEmpty() && members.all { isOwn(it, path + tag) }
                }
                else -> OwnServers.isOwnOutbound(outbound)
            }
            verdicts[tag] = verdict
            return verdict
        }

        val removed = LinkedHashMap<String, List<String>>()
        val ownOnly = ArrayList<String>()
        val updated = outbounds.map { element ->
            val group = element as? JsonObject ?: return@map element
            if (group.text("type") != "urltest") return@map element
            val members = group.members()
            val own = members.filter { isOwn(it, emptySet()) }.toSet()
            val tag = group.text("tag").orEmpty()
            when {
                own.isEmpty() -> element
                members.all { it in own } -> {
                    ownOnly += tag
                    element
                }
                else -> {
                    removed[tag] = members.filter { it in own }
                    val kept = (group["outbounds"] as JsonArray).filterNot {
                        (it as? JsonPrimitive)?.content in own
                    }
                    JsonObject(group + ("outbounds" to JsonArray(kept)))
                }
            }
        }
        if (removed.isEmpty()) return Result(config, removed, ownOnly)
        return Result(JsonObject(root + ("outbounds" to JsonArray(updated))).toString(), removed, ownOnly)
    }

    private fun JsonObject.members(): List<String> =
        (this["outbounds"] as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.content }.orEmpty()
}

private fun JsonObject.text(key: String): String? = (this[key] as? JsonPrimitive)?.content

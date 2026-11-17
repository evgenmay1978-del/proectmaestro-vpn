package com.maestrovpn.tv.whitelist

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Replaces the panel's tun stack with gVisor.
 *
 * Why it exists (задача 18.11.2026: с включённым VPN на Samsung SM-S948B / Android 17 / arm64
 * у владельца мертвы Telegram и YouTube, при этом DNS резолвится): профиль панели пинит
 * `"stack": "system"` на tun-inbound. Стек `system` делает L3→L4 трансляцию СИСТЕМНЫМ сетевым
 * стеком (документация sing-box, tun inbound, `stack`), то есть TCP приходится «сплайсить» в
 * сокеты ядра; на Android без root этот путь не отдаёт ничего. Замер на сборке r689,
 * 18.11 00:15–00:19 (diag/engine.log, 2,5 минуты, в include-списке YouTube/Telegram/Chrome):
 *   inbound packet connection (UDP) = 100, inbound DNS packet = 192,
 *   inbound connection (TCP)        = 0,
 *   совпадения роутера: {=> sniff} 50, {network=udp port=443 => reject} 50 — больше никаких.
 * Значит в tun пришёл только UDP: каждый TCP-сокет каждого приложения молча терялся до движка —
 * ровно та картина «резолвится, но не подключается», на которую жалуется владелец. Исходящее
 * плечо движка в том же логе было живым (195 urltest-проб через выбранный узел и 12 DoT-запросов
 * на 8.8.8.8:853 с ответами), так что узлы и прокси тут ни при чём.
 *
 * `gvisor` делает ту же трансляцию внутри собственного netstack libbox — без сплайсинга в ядро,
 * без root и без iptables. `mixed` заменой НЕ является: по тем же докам это «mixed system TCP
 * stack and gvisor UDP stack», то есть сломанная TCP-половина остаётся как есть.
 *
 * Трогается только распознанная форма: нет inbounds, нет tun-inbound, неразбираемый JSON или
 * tun, у которого уже стоит gvisor, — возвращаются байт в байт. Чистая строка на входе и выходе;
 * вызывающая сторона проверяет результат через Libbox.checkConfig и при отказе ядра оставляет
 * исходный конфиг.
 */
internal object TunStack {
    /** Собственный userspace-netstack sing-tun: единственный стек без сплайсинга в ядро. */
    const val DEFAULT = "gvisor"

    /** Значения, которые уводят TCP в системный сетевой стек. */
    private val SYSTEM_STACKS = setOf("", "system", "auto")

    fun ensure(base: String): String {
        val root = runCatching { Json.parseToJsonElement(base) as? JsonObject }.getOrNull() ?: return base
        val inbounds = root["inbounds"] as? JsonArray ?: return base
        var changed = false
        val updated = inbounds.map { element ->
            val inbound = element as? JsonObject ?: return@map element
            if ((inbound["type"] as? JsonPrimitive)?.content != "tun") return@map element
            val stack = (inbound["stack"] as? JsonPrimitive)?.content?.trim()?.lowercase() ?: ""
            if (stack !in SYSTEM_STACKS) return@map element
            changed = true
            JsonObject(inbound + ("stack" to JsonPrimitive(DEFAULT)))
        }
        if (!changed) return base
        return JsonObject(root + ("inbounds" to JsonArray(updated))).toString()
    }

    /** Стек, который увидит движок в [config] — для строк диагностики. */
    fun effective(config: String): String = runCatching {
        val root = Json.parseToJsonElement(config) as? JsonObject ?: return@runCatching "?"
        val tun = (root["inbounds"] as? JsonArray)?.firstOrNull { element ->
            ((element as? JsonObject)?.get("type") as? JsonPrimitive)?.content == "tun"
        } as? JsonObject ?: return@runCatching "-"
        (tun["stack"] as? JsonPrimitive)?.content ?: "-"
    }.getOrDefault("?")
}

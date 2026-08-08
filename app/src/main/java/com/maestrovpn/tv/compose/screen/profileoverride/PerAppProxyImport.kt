package com.maestrovpn.tv.compose.screen.profileoverride

/**
 * Разбор списка пакетов, вставленного из буфера обмена, на экране «split»
 * (раздельное туннелирование).
 *
 * ⛔ ПОЧЕМУ ЭТО ОТДЕЛЬНАЯ ФУНКЦИЯ, А НЕ ВЫРАЖЕНИЕ В КОМПОЗАБЛЕ (2026-08-03).
 * Раньше в двух местах (телефонная и ТВ-ветки) стояло `clipboardText?.split("\n")?.distinct()`
 * БЕЗ trim. Список, скопированный с Windows или из десктопного редактора, приезжает с CRLF,
 * поэтому каждая строка становилась "com.example\r" и не совпадала НИ С ОДНИМ установленным
 * пакетом. Итог для клиента: показывался УСПЕШНЫЙ тост, в Settings.perAppProxyList уходило
 * пустое множество, а onBack на маршруте `split` гасил perAppProxyEnabled — то есть импорт
 * молча ВЫКЛЮЧАЛ раздельное туннелирование.
 *
 * Функция намеренно без единого android-типа: только такой код можно накрыть JVM-юнит-тестом,
 * а он у нас единственный, который реально выполняется в CI.
 *
 * @return список имён пакетов либо null, если в буфере нет ничего пригодного.
 */
internal fun parseImportedPackageNames(clipboard: String?): List<String>? {
    val names = clipboard
        ?.split('\n')
        // trim() снимает \r от CRLF, пробелы и табуляции — ровно то, чего не хватало.
        ?.map { it.trim() }
        ?.filter { it.isNotEmpty() }
        ?.distinct()
    return if (names.isNullOrEmpty()) null else names
}

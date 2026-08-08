package com.maestrovpn.tv.compose.screen.profileoverride

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Пин на дефект, из-за которого импорт списка молча выключал раздельное туннелирование:
 * буфер с CRLF (Windows/десктоп) давал имена вида "com.example\r", они не совпадали ни с одним
 * установленным пакетом, и в настройки уходило пустое множество — при успешном тосте.
 */
class PerAppProxyImportTest {

    @Test
    fun `CRLF из Windows не ломает разбор`() {
        val parsed = parseImportedPackageNames("com.example.one\r\ncom.example.two\r\n")
        assertEquals(listOf("com.example.one", "com.example.two"), parsed)
    }

    @Test
    fun `обычные переводы строк работают как прежде`() {
        assertEquals(listOf("com.a", "com.b"), parseImportedPackageNames("com.a\ncom.b"))
    }

    @Test
    fun `пробелы по краям и пустые строки отбрасываются`() {
        assertEquals(
            listOf("com.a", "com.b"),
            parseImportedPackageNames("  com.a  \n\n\t\ncom.b\n   \n"),
        )
    }

    @Test
    fun `дубликаты схлопываются`() {
        assertEquals(listOf("com.a"), parseImportedPackageNames("com.a\ncom.a\r\ncom.a"))
    }

    @Test
    fun `пустой и мусорный буфер дают null, а не пустой список`() {
        assertNull(parseImportedPackageNames(null))
        assertNull(parseImportedPackageNames(""))
        assertNull(parseImportedPackageNames("\r\n \n\t"))
    }
}

package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Порядок и подписи секторов дуги. Это единственная часть новой деки, которая реально
 * исполняется в CI (`testOtherDebugUnitTest`): instrumentation-тесты ни один workflow пока
 * не запускает, поэтому контракт меню закрывается здесь.
 */
class PhoneHomeProtocolOrderTest {
    @Test
    fun ownerReferenceOrderIsStableRegardlessOfBackendOrder() {
        val backend = listOf("naive", "auto", "anytls", "vless", "hysteria2")

        assertEquals(
            listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "awg", "olcrtc"),
            orderedHomeProtocols(backend),
        )
    }

    @Test
    fun wdttKeepsItsSectorBetweenNaiveproxyAndWebrtc() {
        val backend = listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn")

        val ordered = orderedHomeProtocols(backend)

        assertEquals(
            listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "awg", "olcrtc"),
            ordered,
        )
        assertTrue("WDTT потерялся из меню телефона", "vk-turn" in ordered)
    }

    @Test
    fun olcrtcIsAlwaysPresentExactlyOnceEvenIfBackendAlreadySentIt() {
        assertEquals(1, orderedHomeProtocols(listOf("auto", "olcrtc")).count { it == "olcrtc" })
        assertEquals(listOf("auto", "olcrtc"), orderedHomeProtocols(listOf("auto", "olcrtc")))
    }

    @Test
    fun emptyRuntimeListKeepsEveryOwnerApprovedArcLabel() {
        assertEquals(
            listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "awg", "olcrtc"),
            orderedHomeProtocols(emptyList()),
        )
    }

    @Test
    fun unknownBackendTagsSurviveAfterTheKnownOnes() {
        val ordered = orderedHomeProtocols(listOf("trojan", "auto", "shadowsocks"))

        assertEquals(listOf("auto", "trojan", "shadowsocks", "vk-turn", "awg", "olcrtc"), ordered)
    }

    @Test
    fun sectorLabelsMatchTheOwnerReferenceCasing() {
        assertEquals("АВТО", homeProtocolLabel("auto"))
        assertEquals("VLESS", homeProtocolLabel("vless"))
        assertEquals("HYSTERIA2", homeProtocolLabel("hysteria2"))
        assertEquals("ANYTLS", homeProtocolLabel("anytls"))
        assertEquals("NAIVEPROXY", homeProtocolLabel("naive"))
        assertEquals("WDTT", homeProtocolLabel("vk-turn"))
        assertEquals("AWG", homeProtocolLabel("awg"))
    }

    @Test
    fun onlyNaiveproxyGetsAManualLineBreakInsideItsSector() {
        // Перенос задан руками ровно там, где слово не влезает: авто-перенос Compose режет
        // «NAIVEPROXY» по символам и оставляет «NAIVEPROX / Y».
        assertEquals("NAIVE\nPROXY", homeProtocolSectorLabel("naive"))
        assertEquals("NAIVEPROXY", homeProtocolLabel("naive"))
        for (tag in listOf("auto", "vless", "hysteria2", "anytls", "vk-turn", "awg", "olcrtc")) {
            assertEquals(homeProtocolLabel(tag), homeProtocolSectorLabel(tag))
        }
    }

    @Test
    fun sevenSectorsSitOnTheOwnerApprovedCentres() {
        val cells = arcSectorCells(7)

        // Центры замерены по арту и отличаются от номинала спеки не больше чем на 2.7 dp
        // (наибольшее расхождение — вторая ячейка: 88.3 против 91). ⛔ Допуск 2.5 dp был мой
        // собственный промах: он не покрывал даже фактический замер, по которому я же и сажал
        // сектора, и падал в CI.
        val nominal = listOf(39f, 91f, 143f, 195f, 247f, 299f, 351f)
        cells.forEachIndexed { i, cell ->
            assertEquals(nominal[i], cell.centerDp, 3f)
        }
        // ⛔ А вот ШИРИНА со спекой не совпадает, и это ловушка: шаг 52 dp обещан, но реальные
        // интерьеры 40…47 dp. Коробка 52 dp выезжала на резной разделитель, и «HYSTERIA2»,
        // «NAIVEPROXY» и «АВТО» стояли криво. Проверяем, что берём замер, а не номинал.
        cells.forEach { assertTrue("ячейка шире реального интерьера", it.widthDp <= 48f) }
        assertEquals(40.1f, cells[0].widthDp, 0.2f)
        assertEquals(40.7f, cells[6].widthDp, 0.2f)
        assertTrue("крайние ячейки уже средних", cells[0].widthDp < cells[3].widthDp - 5f)
        // Центральная ячейка ниже соседних: над ней замок.
        assertTrue("центр ниже соседей", cells[3].topDp > cells[2].topDp)
        assertTrue("центр ниже соседей", cells[3].topDp > cells[4].topDp)
    }

    @Test
    fun fewerProtocolsFillTheCentralCellsNotTheLeftEdge() {
        // Веер симметричен: сдвиг ряда влево оставил бы пустую резьбу сбоку, и это
        // читается как брак сборки, а не как «протоколов меньше».
        assertEquals(listOf(141.7f, 195.6f, 248.2f), arcSectorCells(3).map { it.centerDp })
        assertEquals(5, arcSectorCells(5).size)
        assertEquals(88.3f, arcSectorCells(5).first().centerDp, 0.01f)
        assertEquals(emptyList<Float>(), arcSectorCells(0).map { it.centerDp })
        assertEquals(7, arcSectorCells(9).size)
    }

    @Test
    fun webrtcIsOnlyALabelAndNeverReplacesTheRuntimeTag() {
        // Владелец подписал сектор `WEBRTC`, но рантайм-тег обязан остаться `olcrtc`:
        // по нему выбирается outbound и по нему же считается замок без кредов.
        assertEquals("WEBRTC", homeProtocolLabel("olcrtc"))
        assertEquals("olcRTC", protocolLabel("olcrtc"))
        assertTrue("olcrtc" in orderedHomeProtocols(listOf("auto")))
        assertTrue("vk-turn" in orderedHomeProtocols(listOf("auto")))
        assertTrue("awg" in orderedHomeProtocols(listOf("auto")))
    }
}

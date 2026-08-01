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
            listOf("auto", "vless", "hysteria2", "anytls", "naive", "olcrtc"),
            orderedHomeProtocols(backend),
        )
    }

    @Test
    fun wdttKeepsItsSectorBetweenNaiveproxyAndWebrtc() {
        val backend = listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn")

        val ordered = orderedHomeProtocols(backend)

        assertEquals(
            listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "olcrtc"),
            ordered,
        )
        assertTrue("WDTT потерялся из меню телефона", "vk-turn" in ordered)
    }

    @Test
    fun olcrtcIsAlwaysPresentExactlyOnceEvenIfBackendAlreadySentIt() {
        assertEquals(1, orderedHomeProtocols(listOf("auto", "olcrtc")).count { it == "olcrtc" })
        assertEquals(listOf("auto", "olcrtc"), orderedHomeProtocols(listOf("auto", "olcrtc")))
        assertEquals(listOf("olcrtc"), orderedHomeProtocols(emptyList()))
    }

    @Test
    fun unknownBackendTagsSurviveAfterTheKnownOnes() {
        val ordered = orderedHomeProtocols(listOf("trojan", "auto", "shadowsocks"))

        assertEquals(listOf("auto", "trojan", "shadowsocks", "olcrtc"), ordered)
    }

    @Test
    fun sectorLabelsMatchTheOwnerReferenceCasing() {
        assertEquals("АВТО", homeProtocolLabel("auto"))
        assertEquals("VLESS", homeProtocolLabel("vless"))
        assertEquals("HYSTERIA2", homeProtocolLabel("hysteria2"))
        assertEquals("ANYTLS", homeProtocolLabel("anytls"))
        assertEquals("NAIVEPROXY", homeProtocolLabel("naive"))
        assertEquals("WDTT", homeProtocolLabel("vk-turn"))
    }

    @Test
    fun onlyNaiveproxyGetsAManualLineBreakInsideItsSector() {
        // Перенос задан руками ровно там, где слово не влезает: авто-перенос Compose режет
        // «NAIVEPROXY» по символам и оставляет «NAIVEPROX / Y».
        assertEquals("NAIVE\nPROXY", homeProtocolSectorLabel("naive"))
        assertEquals("NAIVEPROXY", homeProtocolLabel("naive"))
        for (tag in listOf("auto", "vless", "hysteria2", "anytls", "vk-turn", "olcrtc")) {
            assertEquals(homeProtocolLabel(tag), homeProtocolSectorLabel(tag))
        }
    }

    @Test
    fun sevenSectorsSitOnTheOwnerApprovedCentres() {
        val cells = arcSectorCells(7)

        assertEquals(
            listOf(39f, 91f, 143f, 195f, 247f, 299f, 351f),
            cells.map { it.centerDp },
        )
        // Провис верха ячейки — парабола, снятая с самого арта: центр не проседает,
        // край уходит вниз примерно на 12 dp. ⛔ Не путать с силуэтом дуги: он провисает
        // на 39.8 dp, и по нему сектор уехал бы с резьбы на бортик.
        assertEquals(0f, cells[3].sagDp, 0.01f)
        assertEquals(11.7f, cells[0].sagDp, 0.3f)
        assertEquals(cells[0].sagDp, cells[6].sagDp, 0.001f)
        assertEquals(cells[1].sagDp, cells[5].sagDp, 0.001f)
    }

    @Test
    fun fewerProtocolsFillTheCentralCellsNotTheLeftEdge() {
        // Веер симметричен: сдвиг ряда влево оставил бы пустую резьбу сбоку, и это
        // читается как брак сборки, а не как «протоколов меньше».
        assertEquals(listOf(143f, 195f, 247f), arcSectorCells(3).map { it.centerDp })
        assertEquals(listOf(91f, 143f, 195f, 247f, 299f), arcSectorCells(5).map { it.centerDp })
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
    }
}

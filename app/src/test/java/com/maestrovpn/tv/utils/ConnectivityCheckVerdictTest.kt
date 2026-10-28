package com.maestrovpn.tv.utils

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The «Проверить соединение» wording must never promise what the probe cannot see: the hosting app
 * is exempt from its own VPN, so the answer covers the direct channel only
 * (app/src/main/java/com/maestrovpn/tv/bg/PackagePolicy.kt).
 */
class ConnectivityCheckVerdictTest {
    @Test
    fun aHealthyNetworkWithATunnelPointsAtTheTunnel() {
        assertEquals(
            "Сеть работает. Если сайты не открываются — смотрите туннель",
            ConnectivityCheck.verdict(online = true, tunnelUp = true),
        )
    }

    @Test
    fun aHealthyNetworkWithoutATunnelIsJustTheNetwork() {
        assertEquals("Сеть работает", ConnectivityCheck.verdict(online = true, tunnelUp = false))
    }

    @Test
    fun aClosedDirectChannelDoesNotPretendToJudgeTheTunnel() {
        assertEquals(
            "Прямой доступ закрыт: туннель этой проверкой не виден",
            ConnectivityCheck.verdict(online = false, tunnelUp = true),
        )
    }

    @Test
    fun nothingAtAllIsReportedAsNothing() {
        assertEquals("Нет соединения", ConnectivityCheck.verdict(online = false, tunnelUp = false))
    }
}

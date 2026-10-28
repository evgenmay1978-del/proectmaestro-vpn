package com.maestrovpn.tv.whitelist

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D7 contract: the CDN edge DNS lookup budget equals the panel request budget (3 s), not 1 s.
 * On cellular the panel round trip measures 2.4-2.8 s (CDN-APP-164-DEFECTS-2026-10-02), so a 1 s
 * lookup budget was systematically out of reach: prepare() threw TimeoutException, BoxService
 * reported "CDN: подключение недоступно" and stopAndAlert tore down the WHOLE tunnel - including
 * the paid ordinary nodes that were still healthy (D7 audit 28.10.2026).
 */
class WhiteListDnsBudgetContractTest {
    @Test
    fun dnsLookupBudgetIsThePanelRequestBudget() {
        val runtime = appFile("src/main/java/com/maestrovpn/tv/whitelist/WhiteListRuntime.kt").readText()
        val session = appFile("src/main/java/com/maestrovpn/tv/whitelist/WhiteListSession.kt").readText()

        val declared = Regex("REQUEST_LIMIT_MS = (\\d+)_?(\\d*)L").find(runtime)
            ?: error("REQUEST_LIMIT_MS not found in WhiteListRuntime.kt")
        val value = (declared.groupValues[1] + declared.groupValues[2]).toLong()
        assertEquals("panel request budget must cover the 2.4-2.8 s cellular RTT (D7)", 3_000L, value)

        val lookupCall = session.lines().single { it.contains("lookup.get(") }
        assertTrue(
            "DNS budget must reuse REQUEST_LIMIT_MS: $lookupCall",
            lookupCall.contains("WhiteListRuntimeClient.REQUEST_LIMIT_MS, TimeUnit.MILLISECONDS"),
        )
        assertFalse("the 1 s DNS budget is the D7 bug", session.contains("lookup.get(1, TimeUnit.SECONDS)"))
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

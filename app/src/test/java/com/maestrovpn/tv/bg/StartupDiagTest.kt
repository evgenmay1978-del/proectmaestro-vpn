package com.maestrovpn.tv.bg

import java.io.File
import java.nio.file.Files
import org.junit.Assert.assertTrue
import org.junit.Test

class StartupDiagTest {
    @Test
    fun linesAreAppendedWithTheirStage() {
        val base = Files.createTempDirectory("diag").toFile()
        StartupDiag.init(base)

        StartupDiag.log("start", "requested")
        StartupDiag.log("probe", "failed failure=timeout elapsed=10000ms")

        val text = requireNotNull(StartupDiag.file()).readText()
        assertTrue("start line missing: $text", text.contains("start requested"))
        assertTrue("probe line missing: $text", text.contains("probe failed failure=timeout elapsed=10000ms"))
        assertTrue("diag must live under <base>/diag", File(base, "diag/startup.log").isFile)
    }

    @Test
    fun theFileIsRotatedInsteadOfGrowingForever() {
        val base = Files.createTempDirectory("diag-rotate").toFile()
        StartupDiag.init(base)
        val bulk = "x".repeat(100_000)

        repeat(4) { StartupDiag.log("bulk", bulk) }

        assertTrue("rotated file missing", File(base, "diag/startup.log.1").isFile)
        assertTrue("live file must stay small", requireNotNull(StartupDiag.file()).length() <= 400_000)
    }

    @Test
    fun anUnsetDirectoryIsNotAnError() {
        StartupDiag.init(null)
        StartupDiag.log("start", "ignored")
        assertTrue("no file without a directory", StartupDiag.file() == null)
    }
}

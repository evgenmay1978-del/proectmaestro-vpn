package com.maestrovpn.tv.bg

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * D10 contract: every writer of an EXISTING profile config publishes it atomically.
 *
 * UpdateProfileWork refuses to write a profile non-atomically (its KDoc at UpdateProfileWork.kt:66-78
 * explains why: a truncate-then-write is visible torn to a concurrent serviceReload, which reads
 * the file bare and stops a LIVE tunnel with an error). The manual/claim/buy/trial/edit paths kept
 * calling File.writeText directly on configs that the running BoxService may be reading — the
 * invariant existed, the writers ignored it.
 *
 * A new profile file has no concurrent reader, so those paths (ClaimViewModel:179,
 * BuyViewModel:246, TrialViewModel:117, NewProfileViewModel, ProfileImportHandler) legitimately
 * stay on writeText; this test pins only the six rewrites of an existing config.
 */
class WriteAtomicContractTest {
    @Test
    fun writeAtomicIsSharedNotPrivate() {
        val source = appFile("src/main/java/com/maestrovpn/tv/bg/UpdateProfileWork.kt").readText()
        assertTrue(
            "the other writers must be able to call it (D10)",
            source.contains("internal fun writeAtomic(file: File, content: String)"),
        )
        assertFalse("writeAtomic must not go back to private", source.contains("private fun writeAtomic("))
    }

    @Test
    fun writeAtomicStillRenamesInsteadOfTruncatingInPlace() {
        val source = appFile("src/main/java/com/maestrovpn/tv/bg/UpdateProfileWork.kt").readText()
        val body = source.substringAfter("internal fun writeAtomic(")
            .substringBefore("suspend fun refreshNow()")
        assertTrue("writes to a sibling tmp file first", body.contains("\".tmp\""))
        assertTrue("publishes with renameTo, never by truncating the live file", body.contains("renameTo(file)"))
    }

    @Test
    fun everyRewriteOfAnExistingProfileConfigIsAtomic() {
        // Files that rewrite a config the running service may be reading right now.
        val atomic = mapOf(
            "src/main/java/com/maestrovpn/tv/compose/screen/dashboard/DashboardViewModel.kt" to "file.writeText(content)",
            "src/main/java/com/maestrovpn/tv/compose/screen/profile/EditProfileViewModel.kt" to "file.writeText(content)",
            "src/main/java/com/maestrovpn/tv/compose/screen/profile/EditProfileContentViewModel.kt" to "File(p.typed.path).writeText(currentContent)",
            "src/main/java/com/maestrovpn/tv/compose/screen/claim/ClaimViewModel.kt" to "File(existing.typed.path).writeText(content)",
            "src/main/java/com/maestrovpn/tv/compose/screen/purchase/BuyViewModel.kt" to "File(existing.typed.path).writeText(content)",
            "src/main/java/com/maestrovpn/tv/compose/screen/trial/TrialViewModel.kt" to "File(existing.typed.path).writeText(content)",
        )
        for ((path, bareWrite) in atomic) {
            val source = appFile(path).readText()
            assertFalse("$path: bare write of a live config is back (D10)", source.contains(bareWrite))
            assertEquals(
                "$path: must publish the config through writeAtomic",
                1,
                Regex("UpdateProfileWork\\.writeAtomic\\(").findAll(source).count(),
            )
        }
    }

    private fun appFile(path: String): File {
        val matches = listOf(File(path), File("app", path)).filter(File::isFile)
        require(matches.size == 1) { "expected one app file for $path, found ${matches.size}" }
        return matches.single()
    }
}

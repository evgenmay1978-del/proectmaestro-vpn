package com.maestrovpn.tv.update

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateStateProjectionRaceTest {
    @Test
    fun newerAttemptCannotOvertakeAnOlderBlockedProjection() {
        val gate = UpdateStateProjectionGate()
        val coordinator = UpdatePromptCoordinator()
        val oldInfo = updateInfo(154)
        val newerInfo = updateInfo(155)
        val visible = AtomicReference<ProjectionSnapshot?>()
        val projectionOrder = Collections.synchronizedList(mutableListOf<Int>())
        val oldProjectionEntered = CountDownLatch(1)
        val releaseOldProjection = CountDownLatch(1)
        val newerThreadCallingGate = CountDownLatch(1)
        val newerMutationEntered = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(2)

        try {
            val oldFuture = executor.submit {
                gate.mutateAndProject<ProjectionSnapshot>(
                    mutate = {
                        coordinator.applyUpdateCheckResult(Result.success(oldInfo))
                        coordinator.projectionSnapshot()
                    },
                    project = { snapshot ->
                        oldProjectionEntered.countDown()
                        check(releaseOldProjection.await(5, TimeUnit.SECONDS)) {
                            "Timed out waiting to release the old projection"
                        }
                        visible.set(snapshot)
                        projectionOrder += snapshot.updateInfo.versionCode
                    },
                )
            }
            assertTrue(oldProjectionEntered.await(5, TimeUnit.SECONDS))

            val newerFuture = executor.submit {
                newerThreadCallingGate.countDown()
                gate.mutateAndProject<ProjectionSnapshot>(
                    mutate = {
                        newerMutationEntered.countDown()
                        val request = coordinator.requestUpdatePrompt(
                            newerInfo,
                            UpdatePromptProvenance.SettingsManual,
                        )
                        checkNotNull(
                            coordinator.beginAttempt(
                                UpdatePromptOffer(
                                    info = request.info,
                                    requestSequence = request.sequence,
                                    provenance = request.provenance,
                                ),
                            ),
                        )
                        coordinator.projectionSnapshot()
                    },
                    project = { snapshot ->
                        visible.set(snapshot)
                        projectionOrder += snapshot.updateInfo.versionCode
                    },
                )
            }
            assertTrue(newerThreadCallingGate.await(5, TimeUnit.SECONDS))

            val newerOvertookBlockedProjection = newerMutationEntered.await(1, TimeUnit.SECONDS)
            releaseOldProjection.countDown()
            oldFuture.get(5, TimeUnit.SECONDS)
            newerFuture.get(5, TimeUnit.SECONDS)

            assertFalse(
                "A newer mutation entered while the older mutation was still projecting",
                newerOvertookBlockedProjection,
            )
            assertEquals(listOf(154, 155), projectionOrder)
            val finalProjection = visible.get()!!
            assertSame(newerInfo, finalProjection.updateInfo)
            assertSame(newerInfo, finalProjection.verifier)
            assertSame(newerInfo, finalProjection.activeAttempt?.offer?.info)
            assertSame(newerInfo, finalProjection.cachedInfo)
        } finally {
            releaseOldProjection.countDown()
            executor.shutdownNow()
        }
    }

    private data class ProjectionSnapshot(
        val updateInfo: UpdateInfo,
        val verifier: UpdateInfo,
        val activeAttempt: UpdateAttempt?,
        val cachedInfo: UpdateInfo,
    )

    private fun UpdatePromptCoordinator.projectionSnapshot(): ProjectionSnapshot {
        val candidate = checkNotNull(candidate)
        return ProjectionSnapshot(
            updateInfo = candidate,
            verifier = activeAttempt?.offer?.info ?: candidate,
            activeAttempt = activeAttempt,
            cachedInfo = candidate,
        )
    }

    private fun updateInfo(versionCode: Int) = UpdateInfo(
        versionCode = versionCode,
        versionName = "1.0.$versionCode",
        downloadUrl = "https://updates.example.invalid/$versionCode.apk",
        releaseUrl = "https://updates.example.invalid/releases/$versionCode",
        releaseNotes = "Update $versionCode",
        isPrerelease = false,
        fileSize = 1234,
        sha256 = "sha256-$versionCode",
    )
}

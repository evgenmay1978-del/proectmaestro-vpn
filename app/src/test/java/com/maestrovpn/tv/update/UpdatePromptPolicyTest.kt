package com.maestrovpn.tv.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePromptPolicyTest {
    @Test
    fun failedInstallKeepsSameVersionEligible() {
        assertEquals(
            153,
            lastShownVersionAfterPromptAction(153, 154, UpdatePromptAction.InstallFailed),
        )
        assertTrue(shouldShowUpdatePrompt(154, 153, requestedVersion = 154))
    }

    @Test
    fun hideDoesNotPersistTheAvailableVersion() {
        assertEquals(
            153,
            lastShownVersionAfterPromptAction(153, 154, UpdatePromptAction.Hide),
        )
    }

    @Test
    fun explicitDeclineSuppressesOnlyThatAutomaticVersion() {
        val lastShown = lastShownVersionAfterPromptAction(153, 154, UpdatePromptAction.Decline)

        assertEquals(154, lastShown)
        assertFalse(shouldShowUpdatePrompt(154, lastShown, requestedVersion = null))
        assertTrue(shouldShowUpdatePrompt(155, lastShown, requestedVersion = null))
    }

    @Test
    fun manualCheckCanReopenExplicitlyDeclinedVersion() {
        assertTrue(shouldShowUpdatePrompt(154, 154, requestedVersion = 154))
    }

    @Test
    fun repeatedEqualManualRequestsCarryExactInfoAndDistinctSequences() {
        val coordinator = UpdatePromptCoordinator()
        val info = updateInfo(versionCode = 154)

        val first = coordinator.requestUpdatePrompt(info, UpdatePromptProvenance.VendorManual)
        val second = coordinator.requestUpdatePrompt(
            info.copy(),
            UpdatePromptProvenance.VendorManual,
        )

        assertTrue(second.sequence > first.sequence)
        assertEquals(154, first.versionCode)
        assertEquals(154, second.versionCode)
        assertSame(info, first.info)
        assertTrue(first.isForced)
        assertTrue(second.isForced)
    }

    @Test
    fun staleClearCannotEraseANewerRequest() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val second = coordinator.requestUpdatePrompt(updateInfo(155), UpdatePromptProvenance.VendorManual)

        assertFalse(coordinator.clearUpdatePrompt(first.sequence))
        assertEquals(second, coordinator.pendingRequest)
        assertTrue(coordinator.clearUpdatePrompt(second.sequence))
        assertNull(coordinator.pendingRequest)
    }

    @Test
    fun newerRequestSurvivesOldDialogClearWhileAttemptIsActive() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val firstOffer = UpdatePromptOffer(first.info, first.sequence, first.provenance)
        val attempt = coordinator.beginAttempt(firstOffer)
        val second = coordinator.requestUpdatePrompt(updateInfo(155), UpdatePromptProvenance.SettingsManual)

        assertFalse(coordinator.clearUpdatePrompt(first.sequence))
        assertEquals(second, coordinator.pendingRequest)
        assertEquals(154, coordinator.candidate?.versionCode)
        assertEquals(attempt, coordinator.activeAttempt)
    }

    @Test
    fun failedAttemptReissuesCapturedInfoWithANewForcedSequence() {
        val coordinator = UpdatePromptCoordinator()
        val original = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val offer = UpdatePromptOffer(original.info, original.sequence, original.provenance)
        val attempt = coordinator.beginAttempt(offer)!!

        assertNull(coordinator.pendingRequest)
        val retry = coordinator.retryFailedAttempt(attempt.id)!!

        assertTrue(retry.sequence > original.sequence)
        assertSame(attempt.offer.info, retry.info)
        assertEquals(UpdatePromptProvenance.ErrorRetry, retry.provenance)
        assertEquals(retry, coordinator.pendingRequest)
        assertNull(coordinator.activeAttempt)
    }

    @Test
    fun failedOldAttemptPreservesANewerPendingRequest() {
        val coordinator = UpdatePromptCoordinator()
        val original = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val attempt = coordinator.beginAttempt(
            UpdatePromptOffer(original.info, original.sequence, original.provenance),
        )!!
        val newer = coordinator.requestUpdatePrompt(updateInfo(155), UpdatePromptProvenance.SettingsManual)

        val next = coordinator.retryFailedAttempt(attempt.id)

        assertEquals(newer, next)
        assertEquals(newer, coordinator.pendingRequest)
        assertEquals(155, coordinator.candidate?.versionCode)
        assertNull(coordinator.activeAttempt)
    }

    @Test
    fun downloadCancelCreatesNoRequestAndSuppressesTheSessionOffer() {
        val coordinator = UpdatePromptCoordinator()
        val info = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(info))
        val session = UpdatePromptSession()
        val offer = session.nextOffer(
            coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = null,
            activeAttempt = coordinator.activeAttempt,
        )!!
        val attempt = coordinator.beginAttempt(offer)!!

        assertTrue(session.close(offer))
        assertTrue(coordinator.cancelAttempt(attempt.id))

        assertNull(coordinator.pendingRequest)
        assertNull(
            session.nextOffer(
                coordinator.candidate,
                lastShownVersion = 153,
                pendingRequest = null,
                activeAttempt = coordinator.activeAttempt,
            ),
        )
    }

    @Test
    fun lateAttemptCompletionCannotAlterANewerAttemptOrRequest() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val attemptOne = coordinator.beginAttempt(
            UpdatePromptOffer(first.info, first.sequence, first.provenance),
        )!!
        assertTrue(coordinator.cancelAttempt(attemptOne.id))

        val second = coordinator.requestUpdatePrompt(
            updateInfo(155),
            UpdatePromptProvenance.SettingsManual,
        )
        val attemptTwo = coordinator.beginAttempt(
            UpdatePromptOffer(second.info, second.sequence, second.provenance),
        )!!
        val newest = coordinator.requestUpdatePrompt(
            updateInfo(156),
            UpdatePromptProvenance.VendorManual,
        )

        assertFalse(coordinator.completeAttempt(attemptOne.id))
        assertEquals(attemptTwo, coordinator.activeAttempt)
        assertEquals(newest, coordinator.pendingRequest)
        assertEquals(155, coordinator.candidate?.versionCode)
    }

    @Test
    fun activeAttemptDefersAutomaticCandidateWithoutChangingVerifierMetadata() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), UpdatePromptProvenance.VendorManual)
        val attempt = coordinator.beginAttempt(
            UpdatePromptOffer(first.info, first.sequence, first.provenance),
        )!!

        coordinator.applyUpdateCheckResult(Result.success(updateInfo(155)))

        assertEquals(154, coordinator.candidate?.versionCode)
        assertTrue(coordinator.cancelAttempt(attempt.id))
        assertEquals(155, coordinator.candidate?.versionCode)
    }

    @Test
    fun transientCheckFailurePreservesCachedCandidate() {
        val coordinator = UpdatePromptCoordinator()
        val cached = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(cached))

        coordinator.applyUpdateCheckResult(Result.failure(IllegalStateException("offline")))

        assertSame(cached, coordinator.candidate)
    }

    @Test
    fun promptGatewayPersistsOnlyExplicitDecline() {
        val coordinator = UpdatePromptCoordinator()
        var persistedVersion = 153
        val writes = mutableListOf<Int>()
        val gateway = UpdatePromptGateway(
            coordinator = coordinator,
            readLastShownVersion = { persistedVersion },
            writeLastShownVersion = { version ->
                persistedVersion = version
                writes += version
            },
        )

        val request = gateway.publishManual(
            updateInfo(154),
            provenance = UpdatePromptProvenance.VendorManual,
        )
        gateway.applyAction(154, UpdatePromptAction.Hide)
        gateway.applyAction(154, UpdatePromptAction.InstallFailed)

        assertEquals(UpdatePromptProvenance.VendorManual, request.provenance)
        assertTrue(writes.isEmpty())
        assertEquals(153, persistedVersion)

        gateway.applyAction(154, UpdatePromptAction.Decline)

        assertEquals(listOf(154), writes)
        assertEquals(154, persistedVersion)
    }

    @Test
    fun recreationReopensCachedNewerVersionButNotDeclinedVersionUnlessForced() {
        val info = updateInfo(154)

        val firstActivity = UpdatePromptSession()
        val firstOffer = firstActivity.nextOffer(
            info,
            lastShownVersion = 153,
            pendingRequest = null,
            activeAttempt = null,
        )!!
        assertTrue(firstActivity.close(firstOffer))
        assertNull(
            firstActivity.nextOffer(
                info,
                lastShownVersion = 153,
                pendingRequest = null,
                activeAttempt = null,
            ),
        )

        val recreatedActivity = UpdatePromptSession()
        assertEquals(
            154,
            recreatedActivity.nextOffer(
                info,
                lastShownVersion = 153,
                pendingRequest = null,
                activeAttempt = null,
            )?.info?.versionCode,
        )
        assertNull(
            UpdatePromptSession().nextOffer(
                info,
                lastShownVersion = 154,
                pendingRequest = null,
                activeAttempt = null,
            ),
        )

        val forced = UpdatePromptRequest(9, info, UpdatePromptProvenance.SettingsManual)
        val forcedOffer = UpdatePromptSession().nextOffer(
            info,
            lastShownVersion = 154,
            pendingRequest = forced,
            activeAttempt = null,
        )
        assertEquals(9L, forcedOffer?.requestSequence)
        assertEquals(UpdatePromptProvenance.SettingsManual, forcedOffer?.provenance)
    }

    @Test
    fun requestedCancellationClassifiesGenericFailureAndReleasesAfterUnwind() {
        var cancelRequests = 0
        var releases = 0
        val gate = UpdateAttemptCancellationGate { releases += 1 }

        gate.requestCancellation { cancelRequests += 1 }

        assertEquals(1, cancelRequests)
        assertTrue(gate.shouldTreatFailureAsCancellation())
        assertEquals(0, releases)

        gate.onJobCompleted(cancelledByCause = false)
        gate.onJobCompleted(cancelledByCause = false)

        assertEquals(1, releases)
    }

    @Test
    fun activityCancellationReleasesWithoutAnExplicitCancelRequest() {
        var releases = 0
        val gate = UpdateAttemptCancellationGate { releases += 1 }

        assertFalse(gate.shouldTreatFailureAsCancellation())
        gate.onJobCompleted(cancelledByCause = true)
        gate.onJobCompleted(cancelledByCause = true)

        assertEquals(1, releases)
    }

    @Test
    fun promptProvenanceDerivesForcedEligibility() {
        assertFalse(UpdatePromptProvenance.Automatic.isForced)
        assertTrue(UpdatePromptProvenance.VendorManual.isForced)
        assertTrue(UpdatePromptProvenance.SettingsManual.isForced)
        assertTrue(UpdatePromptProvenance.ErrorRetry.isForced)
    }

    @Test
    fun updateTransactionClosesOfferOnlyAfterAttemptLockSucceeds() {
        val coordinator = UpdatePromptCoordinator()
        val info = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(info))
        val session = UpdatePromptSession()
        val offer = session.nextOffer(info, 153, pendingRequest = null, activeAttempt = null)!!
        val workerAttempt = coordinator.beginBackgroundAttempt(updateInfo(155))!!

        val blocked = beginPromptAttemptTransaction(
            offer = offer,
            beginAttempt = coordinator::beginAttempt,
            closeOffer = session::close,
        )

        assertNull(blocked)
        assertSame(offer, session.activeOffer)

        assertTrue(coordinator.cancelAttempt(workerAttempt.id))
        val started = beginPromptAttemptTransaction(
            offer = offer,
            beginAttempt = coordinator::beginAttempt,
            closeOffer = session::close,
        )

        assertEquals(154, started?.offer?.info?.versionCode)
        assertNull(session.activeOffer)
    }

    @Test
    fun terminalFailureReleasesLeaseAndRetrySurvivesActivityRecreation() {
        val coordinator = UpdatePromptCoordinator()
        val request = coordinator.requestUpdatePrompt(
            updateInfo(154),
            UpdatePromptProvenance.VendorManual,
        )
        val oldSession = UpdatePromptSession()
        val offer = oldSession.nextOffer(
            candidate = coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = request,
            activeAttempt = null,
        )!!
        val attempt = coordinator.beginAttempt(offer)!!

        val retry = coordinator.retryFailedAttempt(attempt.id)

        assertNull(coordinator.activeAttempt)
        assertEquals(UpdatePromptProvenance.ErrorRetry, retry?.provenance)
        val recreatedOffer = UpdatePromptSession().nextOffer(
            candidate = coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = coordinator.pendingRequest,
            activeAttempt = coordinator.activeAttempt,
        )
        assertEquals(retry?.sequence, recreatedOffer?.requestSequence)
        assertEquals(154, recreatedOffer?.info?.versionCode)
    }

    @Test
    fun recreatedSessionDoesNotOfferOrSuppressWhileBackgroundAttemptIsActive() {
        val coordinator = UpdatePromptCoordinator()
        val cached = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(cached))
        val backgroundAttempt = coordinator.beginBackgroundAttempt(cached)!!
        val forcedRequest = coordinator.requestUpdatePrompt(
            updateInfo(155),
            provenance = UpdatePromptProvenance.SettingsManual,
        )
        val recreated = UpdatePromptSession()

        assertNull(
            recreated.nextOffer(
                candidate = coordinator.candidate,
                lastShownVersion = 153,
                pendingRequest = forcedRequest,
                activeAttempt = coordinator.activeAttempt,
            ),
        )
        assertNull(recreated.activeOffer)

        assertTrue(coordinator.completeAttempt(backgroundAttempt.id))
        val recovered = recreated.nextOffer(
            candidate = coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = coordinator.pendingRequest,
            activeAttempt = coordinator.activeAttempt,
        )

        assertEquals(155, recovered?.info?.versionCode)
        assertEquals(UpdatePromptProvenance.SettingsManual, recovered?.provenance)
    }

    @Test
    fun sessionReplacesStaleAutomaticOfferAndCoordinatorRejectsOldSnapshot() {
        val coordinator = UpdatePromptCoordinator()
        val oldInfo = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(oldInfo))
        val session = UpdatePromptSession()
        val oldOffer = session.nextOffer(
            candidate = coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = null,
            activeAttempt = null,
        )!!

        val newInfo = updateInfo(155)
        coordinator.applyUpdateCheckResult(Result.success(newInfo))
        val refreshedOffer = session.nextOffer(
            candidate = coordinator.candidate,
            lastShownVersion = 153,
            pendingRequest = null,
            activeAttempt = null,
        )!!

        assertEquals(155, refreshedOffer.info.versionCode)
        assertNull(coordinator.beginAttempt(oldOffer))
        assertEquals(newInfo, coordinator.candidate)
        assertEquals(155, coordinator.beginAttempt(refreshedOffer)?.offer?.info?.versionCode)
    }

    @Test
    fun coordinatorRejectsStaleOrMetadataMismatchedForcedOffer() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(
            updateInfo(154),
            UpdatePromptProvenance.VendorManual,
        )
        val forgedInfo = first.info.copy(downloadUrl = "https://mirror.example.invalid/154.apk")
        val mismatchedOffer = UpdatePromptOffer(
            forgedInfo,
            first.sequence,
            first.provenance,
        )

        assertNull(coordinator.beginAttempt(mismatchedOffer))
        assertEquals(first, coordinator.pendingRequest)

        val newer = coordinator.requestUpdatePrompt(
            updateInfo(155),
            UpdatePromptProvenance.SettingsManual,
        )
        val staleOffer = UpdatePromptOffer(first.info, first.sequence, first.provenance)

        assertNull(coordinator.beginAttempt(staleOffer))
        assertEquals(newer, coordinator.pendingRequest)
        assertNull(coordinator.activeAttempt)
    }

    @Test
    fun backgroundAttemptRejectsStaleCandidateAndPendingMetadataMismatch() {
        val coordinator = UpdatePromptCoordinator()
        val current = updateInfo(155)
        coordinator.applyUpdateCheckResult(Result.success(current))

        assertNull(coordinator.beginBackgroundAttempt(updateInfo(154)))
        assertEquals(current, coordinator.candidate)
        assertNull(coordinator.activeAttempt)

        val pendingInfo = updateInfo(156)
        val pending = coordinator.requestUpdatePrompt(
            pendingInfo,
            UpdatePromptProvenance.VendorManual,
        )
        val workerInfo = pendingInfo.copy(
            downloadUrl = "https://mirror.example.invalid/156.apk",
            sha256 = "different156",
        )
        coordinator.applyUpdateCheckResult(Result.success(workerInfo))

        assertEquals(pendingInfo, coordinator.candidate)
        assertNull(coordinator.beginBackgroundAttempt(workerInfo))
        assertEquals(pending, coordinator.pendingRequest)
        assertNull(coordinator.activeAttempt)
    }

    @Test
    fun staleAutomaticResultCannotRollbackCurrentOrDeferredCandidate() {
        val coordinator = UpdatePromptCoordinator()
        val current = updateInfo(155)
        coordinator.applyUpdateCheckResult(Result.success(current))

        coordinator.applyUpdateCheckResult(Result.success(updateInfo(154)))

        assertEquals(current, coordinator.candidate)
        assertNull(coordinator.beginBackgroundAttempt(updateInfo(154)))

        val currentAttempt = coordinator.beginBackgroundAttempt(current)!!
        coordinator.applyUpdateCheckResult(Result.success(updateInfo(154)))
        assertTrue(coordinator.cancelAttempt(currentAttempt.id))

        assertEquals(current, coordinator.candidate)
    }

    @Test
    fun projectionFailureRollsBackNewlyAcquiredAttemptBeforeEscaping() {
        val coordinator = UpdatePromptCoordinator()
        val info = updateInfo(154)
        coordinator.applyUpdateCheckResult(Result.success(info))
        var rollbackCount = 0
        var thrown: Throwable? = null

        try {
            acquireUpdateAttemptSafely(
                acquire = { coordinator.beginBackgroundAttempt(info) },
                project = { throw IllegalStateException("cache write failed") },
                rollback = { attemptId ->
                    rollbackCount += 1
                    coordinator.cancelAttempt(attemptId)
                },
            )
        } catch (error: Throwable) {
            thrown = error
        }

        assertEquals("cache write failed", thrown?.message)
        assertEquals(1, rollbackCount)
        assertNull(coordinator.activeAttempt)
        assertEquals(info, coordinator.candidate)
    }

    @Test
    fun backgroundAttemptCannotReplaceAnActiveUiAttemptOrItsVerifierCandidate() {
        val coordinator = UpdatePromptCoordinator()
        val uiInfo = updateInfo(154)
        val uiRequest = coordinator.requestUpdatePrompt(
            uiInfo,
            provenance = UpdatePromptProvenance.VendorManual,
        )
        val uiAttempt = coordinator.beginAttempt(
            UpdatePromptOffer(uiRequest.info, uiRequest.sequence, uiRequest.provenance),
        )!!

        assertNull(coordinator.beginBackgroundAttempt(updateInfo(155)))
        assertSame(uiAttempt, coordinator.activeAttempt)
        assertSame(uiInfo, coordinator.candidate)

        coordinator.clearAll()

        assertSame(uiAttempt, coordinator.activeAttempt)
        assertSame(uiInfo, coordinator.candidate)
        assertTrue(coordinator.cancelAttempt(uiAttempt.id))
        assertNull(coordinator.candidate)
    }

    @Test
    fun invalidatedActiveAttemptReleasesWithoutReofferingBadArchive() {
        val coordinator = UpdatePromptCoordinator()
        val request = coordinator.requestUpdatePrompt(
            updateInfo(154),
            UpdatePromptProvenance.VendorManual,
        )
        val attempt = coordinator.beginAttempt(
            UpdatePromptOffer(request.info, request.sequence, request.provenance),
        )!!

        coordinator.clearAll()

        assertNull(coordinator.retryFailedAttempt(attempt.id))
        assertNull(coordinator.activeAttempt)
        assertNull(coordinator.candidate)
        assertNull(coordinator.pendingRequest)
    }

    @Test
    fun newerManualRequestConsumesAncientDeferredClearBeforeItsAttempt() {
        val coordinator = UpdatePromptCoordinator()
        val oldRequest = coordinator.requestUpdatePrompt(
            updateInfo(154),
            UpdatePromptProvenance.VendorManual,
        )
        val oldAttempt = coordinator.beginAttempt(
            UpdatePromptOffer(oldRequest.info, oldRequest.sequence, oldRequest.provenance),
        )!!
        coordinator.clearAll()
        val newer = coordinator.requestUpdatePrompt(
            updateInfo(155),
            UpdatePromptProvenance.SettingsManual,
        )

        assertEquals(newer, coordinator.retryFailedAttempt(oldAttempt.id))
        val newerAttempt = coordinator.beginAttempt(
            UpdatePromptOffer(newer.info, newer.sequence, newer.provenance),
        )!!
        assertTrue(coordinator.cancelAttempt(newerAttempt.id))

        assertEquals(155, coordinator.candidate?.versionCode)
    }

    @Test
    fun automaticCheckAfterManualRequestRemainsDeferredUntilAttemptFinishes() {
        val coordinator = UpdatePromptCoordinator()
        val oldRequest = coordinator.requestUpdatePrompt(
            updateInfo(154),
            UpdatePromptProvenance.VendorManual,
        )
        val oldAttempt = coordinator.beginAttempt(
            UpdatePromptOffer(oldRequest.info, oldRequest.sequence, oldRequest.provenance),
        )!!
        coordinator.clearAll()
        val manual = coordinator.requestUpdatePrompt(
            updateInfo(155),
            UpdatePromptProvenance.SettingsManual,
        )
        coordinator.applyUpdateCheckResult(Result.success(updateInfo(156)))

        assertEquals(manual, coordinator.retryFailedAttempt(oldAttempt.id))
        val manualAttempt = coordinator.beginAttempt(
            UpdatePromptOffer(manual.info, manual.sequence, manual.provenance),
        )!!
        assertTrue(coordinator.cancelAttempt(manualAttempt.id))

        assertEquals(156, coordinator.candidate?.versionCode)
    }

    private fun updateInfo(versionCode: Int) = UpdateInfo(
        versionCode = versionCode,
        versionName = "1.0.$versionCode",
        downloadUrl = "https://updates.example.invalid/$versionCode.apk",
        releaseUrl = "https://updates.example.invalid/releases/$versionCode",
        releaseNotes = "Update $versionCode",
        isPrerelease = false,
        fileSize = 1234,
        sha256 = "abc$versionCode",
    )
}

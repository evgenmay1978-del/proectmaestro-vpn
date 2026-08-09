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

        val first = coordinator.requestUpdatePrompt(info, forced = true)
        val second = coordinator.requestUpdatePrompt(info.copy(), forced = true)

        assertTrue(second.sequence > first.sequence)
        assertEquals(154, first.versionCode)
        assertEquals(154, second.versionCode)
        assertSame(info, first.info)
        assertTrue(first.forced)
        assertTrue(second.forced)
    }

    @Test
    fun staleClearCannotEraseANewerRequest() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val second = coordinator.requestUpdatePrompt(updateInfo(155), forced = true)

        assertFalse(coordinator.clearUpdatePrompt(first.sequence))
        assertEquals(second, coordinator.pendingRequest)
        assertTrue(coordinator.clearUpdatePrompt(second.sequence))
        assertNull(coordinator.pendingRequest)
    }

    @Test
    fun newerRequestSurvivesOldDialogClearWhileAttemptIsActive() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val firstOffer = UpdatePromptOffer(first.info, first.sequence, forced = true)
        val attempt = coordinator.beginAttempt(firstOffer)
        val second = coordinator.requestUpdatePrompt(updateInfo(155), forced = true)

        assertFalse(coordinator.clearUpdatePrompt(first.sequence))
        assertEquals(second, coordinator.pendingRequest)
        assertEquals(154, coordinator.candidate?.versionCode)
        assertEquals(attempt, coordinator.activeAttempt)
    }

    @Test
    fun failedAttemptReissuesCapturedInfoWithANewForcedSequence() {
        val coordinator = UpdatePromptCoordinator()
        val original = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val offer = UpdatePromptOffer(original.info, original.sequence, forced = true)
        val attempt = coordinator.beginAttempt(offer)!!

        assertNull(coordinator.pendingRequest)
        val retry = coordinator.retryFailedAttempt(attempt.id)!!

        assertTrue(retry.sequence > original.sequence)
        assertSame(attempt.offer.info, retry.info)
        assertTrue(retry.forced)
        assertEquals(retry, coordinator.pendingRequest)
        assertNull(coordinator.activeAttempt)
    }

    @Test
    fun failedOldAttemptPreservesANewerPendingRequest() {
        val coordinator = UpdatePromptCoordinator()
        val original = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val attempt = coordinator.beginAttempt(
            UpdatePromptOffer(original.info, original.sequence, forced = true),
        )!!
        val newer = coordinator.requestUpdatePrompt(updateInfo(155), forced = true)

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
        val offer = session.nextOffer(coordinator.candidate, lastShownVersion = 153, pendingRequest = null)!!
        val attempt = coordinator.beginAttempt(offer)!!

        assertTrue(session.close(offer))
        assertTrue(coordinator.cancelAttempt(attempt.id))

        assertNull(coordinator.pendingRequest)
        assertNull(session.nextOffer(coordinator.candidate, lastShownVersion = 153, pendingRequest = null))
    }

    @Test
    fun lateAttemptCompletionCannotAlterANewerAttemptOrRequest() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val attemptOne = coordinator.beginAttempt(UpdatePromptOffer(first.info, first.sequence, true))!!
        assertTrue(coordinator.cancelAttempt(attemptOne.id))

        val second = coordinator.requestUpdatePrompt(updateInfo(155), forced = true)
        val attemptTwo = coordinator.beginAttempt(UpdatePromptOffer(second.info, second.sequence, true))!!
        val newest = coordinator.requestUpdatePrompt(updateInfo(156), forced = true)

        assertFalse(coordinator.completeAttempt(attemptOne.id))
        assertEquals(attemptTwo, coordinator.activeAttempt)
        assertEquals(newest, coordinator.pendingRequest)
        assertEquals(155, coordinator.candidate?.versionCode)
    }

    @Test
    fun activeAttemptDefersAutomaticCandidateWithoutChangingVerifierMetadata() {
        val coordinator = UpdatePromptCoordinator()
        val first = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)
        val attempt = coordinator.beginAttempt(UpdatePromptOffer(first.info, first.sequence, true))!!

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
    fun manualPromptGatewayDoesNotAdvanceLastShownBeforeUserAction() {
        val coordinator = UpdatePromptCoordinator()
        val currentLastShown = 154

        val request = coordinator.requestUpdatePrompt(updateInfo(154), forced = true)

        assertEquals(154, currentLastShown)
        assertEquals(
            currentLastShown,
            lastShownVersionAfterPromptAction(
                currentLastShown,
                request.versionCode,
                UpdatePromptAction.Hide,
            ),
        )
    }

    @Test
    fun recreationReopensCachedNewerVersionButNotDeclinedVersionUnlessForced() {
        val info = updateInfo(154)

        val firstActivity = UpdatePromptSession()
        val firstOffer = firstActivity.nextOffer(info, lastShownVersion = 153, pendingRequest = null)!!
        assertTrue(firstActivity.close(firstOffer))
        assertNull(firstActivity.nextOffer(info, lastShownVersion = 153, pendingRequest = null))

        val recreatedActivity = UpdatePromptSession()
        assertEquals(154, recreatedActivity.nextOffer(info, lastShownVersion = 153, pendingRequest = null)?.info?.versionCode)
        assertNull(UpdatePromptSession().nextOffer(info, lastShownVersion = 154, pendingRequest = null))

        val forced = UpdatePromptRequest(sequence = 9, info = info, forced = true)
        val forcedOffer = UpdatePromptSession().nextOffer(info, lastShownVersion = 154, pendingRequest = forced)
        assertEquals(9L, forcedOffer?.requestSequence)
        assertTrue(forcedOffer?.forced == true)
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

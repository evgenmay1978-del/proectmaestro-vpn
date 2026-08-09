package com.maestrovpn.tv.update

internal enum class UpdatePromptAction { Hide, Decline, InstallFailed }

internal fun lastShownVersionAfterPromptAction(
    current: Int,
    available: Int,
    action: UpdatePromptAction,
): Int = if (action == UpdatePromptAction.Decline) maxOf(current, available) else current

internal fun shouldShowUpdatePrompt(
    availableVersion: Int?,
    lastShownVersion: Int,
    requestedVersion: Int?,
): Boolean = availableVersion != null &&
    (availableVersion > lastShownVersion || requestedVersion == availableVersion)

internal data class UpdatePromptRequest(
    val sequence: Long,
    val info: UpdateInfo,
    val forced: Boolean,
) {
    val versionCode: Int
        get() = info.versionCode
}

internal data class UpdatePromptOffer(
    val info: UpdateInfo,
    val requestSequence: Long?,
    val forced: Boolean,
)

internal data class UpdateAttempt(
    val id: Long,
    val offer: UpdatePromptOffer,
)

/**
 * Pure state machine behind [UpdateState]. While an install attempt is active, [candidate]
 * remains the exact metadata captured by that attempt because ApkDownloader verifies against it.
 */
internal class UpdatePromptCoordinator(
    initialSequence: Long = 0,
) {
    private sealed interface DeferredCheck {
        data class Completed(val info: UpdateInfo?) : DeferredCheck
    }

    private var nextSequence = initialSequence
    private var nextAttemptId = 0L
    private var deferredCheck: DeferredCheck? = null

    var candidate: UpdateInfo? = null
        private set

    var pendingRequest: UpdatePromptRequest? = null
        private set

    var activeAttempt: UpdateAttempt? = null
        private set

    @Synchronized
    fun applyUpdateCheckResult(result: Result<UpdateInfo?>) {
        if (result.isFailure) return
        val completed = DeferredCheck.Completed(result.getOrNull())
        if (activeAttempt != null) {
            deferredCheck = completed
        } else {
            candidate = completed.info
        }
    }

    @Synchronized
    fun requestUpdatePrompt(info: UpdateInfo, forced: Boolean): UpdatePromptRequest {
        val request = UpdatePromptRequest(
            sequence = ++nextSequence,
            info = info,
            forced = forced,
        )
        pendingRequest = request
        if (activeAttempt == null) candidate = info
        return request
    }

    @Synchronized
    fun clearUpdatePrompt(sequence: Long): Boolean {
        if (pendingRequest?.sequence != sequence) return false
        pendingRequest = null
        applyDeferredIfIdle()
        return true
    }

    @Synchronized
    fun beginAttempt(offer: UpdatePromptOffer): UpdateAttempt? {
        if (activeAttempt != null) return null
        offer.requestSequence?.let { consumedSequence ->
            if (pendingRequest?.sequence == consumedSequence) pendingRequest = null
        }
        val attempt = UpdateAttempt(++nextAttemptId, offer)
        activeAttempt = attempt
        candidate = offer.info
        return attempt
    }

    @Synchronized
    fun completeAttempt(attemptId: Long): Boolean = finishAttempt(attemptId)

    @Synchronized
    fun cancelAttempt(attemptId: Long): Boolean = finishAttempt(attemptId)

    @Synchronized
    fun retryFailedAttempt(attemptId: Long): UpdatePromptRequest? {
        val attempt = activeAttempt?.takeIf { it.id == attemptId } ?: return null
        activeAttempt = null
        pendingRequest?.let { newerRequest ->
            candidate = newerRequest.info
            return newerRequest
        }
        return requestUpdatePrompt(attempt.offer.info, forced = true)
    }

    @Synchronized
    fun clearAll() {
        candidate = null
        pendingRequest = null
        activeAttempt = null
        deferredCheck = null
    }

    private fun finishAttempt(attemptId: Long): Boolean {
        if (activeAttempt?.id != attemptId) return false
        activeAttempt = null
        val pending = pendingRequest
        if (pending != null) {
            candidate = pending.info
        } else {
            applyDeferredIfIdle()
        }
        return true
    }

    private fun applyDeferredIfIdle() {
        if (activeAttempt != null || pendingRequest != null) return
        val deferred = deferredCheck as? DeferredCheck.Completed ?: return
        deferredCheck = null
        candidate = deferred.info
    }
}

/** Per-Activity suppression. Recreating the Activity intentionally creates a fresh session. */
internal class UpdatePromptSession {
    private val suppressedVersions = mutableSetOf<Int>()

    var activeOffer: UpdatePromptOffer? = null
        private set

    fun nextOffer(
        candidate: UpdateInfo?,
        lastShownVersion: Int,
        pendingRequest: UpdatePromptRequest?,
    ): UpdatePromptOffer? {
        val request = pendingRequest
        if (request != null && request.forced) {
            val current = activeOffer
            if (current?.requestSequence == request.sequence) return current
            return UpdatePromptOffer(request.info, request.sequence, forced = true).also {
                activeOffer = it
            }
        }

        activeOffer?.let { return it }
        val requestedVersion = request?.takeIf { it.forced }?.versionCode
        if (candidate == null || candidate.versionCode in suppressedVersions) return null
        if (!shouldShowUpdatePrompt(candidate.versionCode, lastShownVersion, requestedVersion)) return null

        return UpdatePromptOffer(
            info = candidate,
            requestSequence = request?.sequence,
            forced = request?.forced == true,
        ).also { activeOffer = it }
    }

    fun close(offer: UpdatePromptOffer): Boolean {
        if (activeOffer != offer) return false
        activeOffer = null
        suppressedVersions += offer.info.versionCode
        return true
    }
}

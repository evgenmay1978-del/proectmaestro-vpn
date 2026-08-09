package com.maestrovpn.tv.update

internal enum class UpdatePromptProvenance(val isForced: Boolean) {
    Automatic(false),
    VendorManual(true),
    SettingsManual(true),
    ErrorRetry(true),
}

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
    val provenance: UpdatePromptProvenance,
) {
    val versionCode: Int
        get() = info.versionCode

    val isForced: Boolean
        get() = provenance.isForced
}

internal data class UpdatePromptOffer(
    val info: UpdateInfo,
    val requestSequence: Long?,
    val provenance: UpdatePromptProvenance,
) {
    val isForced: Boolean
        get() = provenance.isForced
}

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
    private var invalidatedAttemptId: Long? = null

    var candidate: UpdateInfo? = null
        private set

    var pendingRequest: UpdatePromptRequest? = null
        private set

    var activeAttempt: UpdateAttempt? = null
        private set

    @Synchronized
    fun applyUpdateCheckResult(result: Result<UpdateInfo?>) {
        if (result.isFailure) return
        val info = result.getOrNull()
        val highestKnownVersion = listOfNotNull(
            candidate?.versionCode,
            pendingRequest?.versionCode,
            activeAttempt?.offer?.info?.versionCode,
            (deferredCheck as? DeferredCheck.Completed)?.info?.versionCode,
        ).maxOrNull()
        if (info != null && highestKnownVersion != null && info.versionCode < highestKnownVersion) {
            return
        }

        val completed = DeferredCheck.Completed(info)
        if (activeAttempt != null || pendingRequest != null) {
            deferredCheck = completed
        } else {
            candidate = completed.info
        }
    }

    @Synchronized
    fun requestUpdatePrompt(
        info: UpdateInfo,
        provenance: UpdatePromptProvenance,
    ): UpdatePromptRequest {
        if (provenance == UpdatePromptProvenance.VendorManual ||
            provenance == UpdatePromptProvenance.SettingsManual
        ) deferredCheck = null
        val request = UpdatePromptRequest(
            sequence = ++nextSequence,
            info = info,
            provenance = provenance,
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
        val requestSequence = offer.requestSequence
        if (requestSequence != null) {
            val request = pendingRequest ?: return null
            if (request.sequence != requestSequence ||
                request.info != offer.info ||
                request.provenance != offer.provenance
            ) return null
            pendingRequest = null
        } else if (pendingRequest != null || candidate != offer.info) {
            return null
        }
        val attempt = UpdateAttempt(++nextAttemptId, offer)
        activeAttempt = attempt
        candidate = offer.info
        return attempt
    }

    @Synchronized
    fun beginBackgroundAttempt(info: UpdateInfo): UpdateAttempt? {
        if (activeAttempt != null) return null
        if (candidate != info) return null
        if (pendingRequest != null) return null
        return beginAttempt(
            UpdatePromptOffer(
                info = info,
                requestSequence = null,
                provenance = UpdatePromptProvenance.Automatic,
            ),
        )
    }

    @Synchronized
    fun completeAttempt(attemptId: Long): Boolean = finishAttempt(attemptId)

    @Synchronized
    fun cancelAttempt(attemptId: Long): Boolean = finishAttempt(attemptId)

    @Synchronized
    fun retryFailedAttempt(attemptId: Long): UpdatePromptRequest? {
        val attempt = activeAttempt?.takeIf { it.id == attemptId } ?: return null
        val invalidated = invalidatedAttemptId == attemptId
        activeAttempt = null
        invalidatedAttemptId = null
        pendingRequest?.let { newerRequest ->
            candidate = newerRequest.info
            return newerRequest
        }
        if (invalidated) {
            applyDeferredIfIdle()
            return null
        }
        return requestUpdatePrompt(
            attempt.offer.info,
            provenance = UpdatePromptProvenance.ErrorRetry,
        )
    }

    @Synchronized
    fun clearAll() {
        if (activeAttempt != null) {
            invalidatedAttemptId = activeAttempt?.id
            pendingRequest = null
            deferredCheck = DeferredCheck.Completed(null)
            return
        }

        candidate = null
        pendingRequest = null
        activeAttempt = null
        deferredCheck = null
        invalidatedAttemptId = null
    }

    private fun finishAttempt(attemptId: Long): Boolean {
        if (activeAttempt?.id != attemptId) return false
        activeAttempt = null
        if (invalidatedAttemptId == attemptId) invalidatedAttemptId = null
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

/**
 * Persistence boundary for prompt actions. Publishing an offer never writes user suppression;
 * only an explicit decline can advance the persisted last-shown version.
 */
internal class UpdatePromptGateway(
    private val coordinator: UpdatePromptCoordinator,
    private val readLastShownVersion: () -> Int,
    private val writeLastShownVersion: (Int) -> Unit,
) {
    fun publishManual(
        info: UpdateInfo,
        provenance: UpdatePromptProvenance,
    ): UpdatePromptRequest {
        require(provenance.isForced) { "Manual prompt provenance must be forced" }
        return coordinator.requestUpdatePrompt(info, provenance)
    }

    fun applyAction(availableVersion: Int, action: UpdatePromptAction): Int {
        val current = readLastShownVersion()
        val next = lastShownVersionAfterPromptAction(current, availableVersion, action)
        if (next != current) writeLastShownVersion(next)
        return next
    }
}

/** Keeps the attempt lease until the cancelled job has actually unwound. */
internal class UpdateAttemptCancellationGate(
    private val releaseAttempt: () -> Unit,
) {
    private var released = false
    private var cancellationRequested = false

    fun requestCancellation(cancelJob: () -> Unit) {
        synchronized(this) {
            cancellationRequested = true
        }
        cancelJob()
    }

    fun shouldTreatFailureAsCancellation(): Boolean = synchronized(this) {
        cancellationRequested
    }

    fun onJobCompleted(cancelledByCause: Boolean) {
        val shouldRelease = synchronized(this) {
            if ((!cancellationRequested && !cancelledByCause) || released) {
                false
            } else {
                released = true
                true
            }
        }
        if (shouldRelease) releaseAttempt()
    }
}

internal fun acquireUpdateAttemptSafely(
    acquire: () -> UpdateAttempt?,
    project: () -> Unit,
    rollback: (Long) -> Unit,
): UpdateAttempt? {
    val attempt = acquire() ?: return null
    try {
        project()
    } catch (error: Throwable) {
        try {
            rollback(attempt.id)
        } catch (rollbackError: Throwable) {
            error.addSuppressed(rollbackError)
        }
        throw error
    }
    return attempt
}

internal fun beginPromptAttemptTransaction(
    offer: UpdatePromptOffer,
    beginAttempt: (UpdatePromptOffer) -> UpdateAttempt?,
    closeOffer: (UpdatePromptOffer) -> Boolean,
    rollbackAttempt: (Long) -> Unit,
): UpdateAttempt? {
    val attempt = beginAttempt(offer) ?: return null
    val closed = try {
        closeOffer(offer)
    } catch (error: Throwable) {
        try {
            rollbackAttempt(attempt.id)
        } catch (rollbackError: Throwable) {
            error.addSuppressed(rollbackError)
        }
        throw error
    }
    if (!closed) {
        rollbackAttempt(attempt.id)
        return null
    }
    return attempt
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
        activeAttempt: UpdateAttempt?,
    ): UpdatePromptOffer? {
        if (activeAttempt != null) {
            activeOffer = null
            return null
        }

        val request = pendingRequest
        if (request != null && request.isForced) {
            val current = activeOffer
            if (current?.requestSequence == request.sequence) return current
            return UpdatePromptOffer(request.info, request.sequence, request.provenance).also {
                activeOffer = it
            }
        }

        activeOffer?.let { current ->
            if (current.requestSequence == null && current.info == candidate) {
                return current
            }
            activeOffer = null
        }
        val requestedVersion = request?.takeIf { it.isForced }?.versionCode
        if (candidate == null || candidate.versionCode in suppressedVersions) return null
        if (!shouldShowUpdatePrompt(candidate.versionCode, lastShownVersion, requestedVersion)) return null

        return UpdatePromptOffer(
            info = candidate,
            requestSequence = request?.sequence,
            provenance = request?.provenance ?: UpdatePromptProvenance.Automatic,
        ).also { activeOffer = it }
    }

    fun close(offer: UpdatePromptOffer): Boolean {
        if (activeOffer != offer) return false
        activeOffer = null
        suppressedVersions += offer.info.versionCode
        return true
    }
}

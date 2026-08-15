package com.maestrovpn.tv.bg

internal sealed class WdttStartState(open val startedAtMs: Long) {
    data class Waiting(
        override val startedAtMs: Long,
        val stage: WdttStage,
    ) : WdttStartState(startedAtMs)

    data class CaptchaRequired(
        override val startedAtMs: Long,
        val requestedAtMs: Long,
        val request: WdttCaptchaRequest,
    ) : WdttStartState(startedAtMs)

    data class Ready(override val startedAtMs: Long) : WdttStartState(startedAtMs)
    data class Failed(
        override val startedAtMs: Long,
        val code: WdttSafeErrorCode,
    ) : WdttStartState(startedAtMs)

    data class Cancelled(override val startedAtMs: Long) : WdttStartState(startedAtMs)
    data class Stopped(override val startedAtMs: Long) : WdttStartState(startedAtMs)
}

internal enum class WdttStartDecision {
    WAIT,
    STARTED,
    STOP,
}

internal fun nextWdttStartDecision(
    state: WdttStartState,
    childAlive: Boolean,
    nowMs: Long,
    ordinaryDeadlineMs: Long,
    captchaDeadlineMs: Long,
): WdttStartDecision {
    if (!childAlive) return WdttStartDecision.STOP
    return when (state) {
        is WdttStartState.Ready -> WdttStartDecision.STARTED
        is WdttStartState.Waiting -> if (
            deadlineReached(state.startedAtMs, nowMs, ordinaryDeadlineMs)
        ) WdttStartDecision.STOP else WdttStartDecision.WAIT
        is WdttStartState.CaptchaRequired -> if (
            deadlineReached(state.requestedAtMs, nowMs, captchaDeadlineMs)
        ) WdttStartDecision.STOP else WdttStartDecision.WAIT
        is WdttStartState.Cancelled,
        is WdttStartState.Failed,
        is WdttStartState.Stopped
        -> WdttStartDecision.STOP
    }
}

private fun deadlineReached(startedAtMs: Long, nowMs: Long, durationMs: Long): Boolean {
    if (startedAtMs < 0L || nowMs < startedAtMs || durationMs <= 0L) return true
    return nowMs - startedAtMs >= durationMs
}

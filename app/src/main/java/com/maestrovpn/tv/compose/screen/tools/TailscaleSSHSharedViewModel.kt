package com.maestrovpn.tv.compose.screen.tools

import com.maestrovpn.tv.compose.base.BaseViewModel
import com.maestrovpn.tv.terminal.TailscaleSSHPresentedSession

data class TailscaleSSHSharedState(
    val pendingSession: TailscaleSSHPresentedSession? = null,
)

class TailscaleSSHSharedViewModel : BaseViewModel<TailscaleSSHSharedState, Nothing>() {
    override fun createInitialState() = TailscaleSSHSharedState()

    fun setPendingSession(session: TailscaleSSHPresentedSession) {
        updateState { copy(pendingSession = session) }
    }

    fun consumePendingSession(): TailscaleSSHPresentedSession? {
        val session = currentState.pendingSession
        updateState { copy(pendingSession = null) }
        return session
    }
}

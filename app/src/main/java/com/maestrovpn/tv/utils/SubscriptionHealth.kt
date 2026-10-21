package com.maestrovpn.tv.utils

import kotlinx.coroutines.flow.MutableStateFlow

/**
 * Server-side health of the subscription, as reported by the silent updater.
 *
 * Only authoritative panel answers raise an alert: 402 (expired) and 404/410 (the token is
 * gone). Network failures and 5xx stay silent — a flaky link must never scare a paying
 * customer or tell them to re-activate a working subscription.
 */
object SubscriptionHealth {
    enum class Kind { EXPIRED, REVOKED }

    data class Alert(val kind: Kind)

    val alert = MutableStateFlow<Alert?>(null)

    /** Pure mapping so the contract is unit-tested. */
    fun classify(status: Int): Kind? = when (status) {
        402 -> Kind.EXPIRED
        404, 410 -> Kind.REVOKED
        else -> null
    }

    fun report(status: Int) {
        val kind = classify(status) ?: return
        alert.value = Alert(kind)
    }

    fun clear() {
        alert.value = null
    }
}

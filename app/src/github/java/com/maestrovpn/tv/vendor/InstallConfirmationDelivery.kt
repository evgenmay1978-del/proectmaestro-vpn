package com.maestrovpn.tv.vendor

internal enum class InstallConfirmationDelivery { Launched, Parked }

internal inline fun <T> deliverInstallConfirmation(
    confirmation: T,
    appInForeground: Boolean,
    launch: (T) -> Unit,
    park: (T) -> Unit,
): InstallConfirmationDelivery {
    if (appInForeground) {
        val launched = runCatching { launch(confirmation) }.isSuccess
        if (launched) return InstallConfirmationDelivery.Launched
    }
    park(confirmation)
    return InstallConfirmationDelivery.Parked
}

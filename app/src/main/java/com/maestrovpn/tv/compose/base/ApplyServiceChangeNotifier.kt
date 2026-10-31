package com.maestrovpn.tv.compose.base

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import com.maestrovpn.tv.aivpn.AivpnTunnel
import com.maestrovpn.tv.constant.Status

/**
 * Offers to apply a settings change to the tunnel that is actually up.
 *
 * AIVPN is a second, independent VpnService, so the old sing-box-only gate silently swallowed
 * every prompt while the neuro tunnel carried the traffic: the user changed the app selection and
 * nothing happened until a manual reconnect. The AIVPN side is read live (not captured by
 * [remember]) because it has no Status to key the composition on.
 */
@Composable
fun rememberApplyServiceChangeNotifier(
    serviceStatus: Status,
): (UiEvent.ApplyServiceChange.Mode) -> Unit = remember(serviceStatus) {
    { mode ->
        if (serviceStatus == Status.Started || AivpnTunnel.isActive) {
            GlobalEventBus.tryEmit(UiEvent.ApplyServiceChange(mode))
        }
    }
}

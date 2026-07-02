package com.maestrovpn.tv.bg

import android.app.KeyguardManager
import android.content.Context
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import androidx.annotation.RequiresApi
import com.maestrovpn.tv.constant.Status

@RequiresApi(24)
class TileService :
    TileService(),
    ServiceConnection.Callback {
    private val connection = ServiceConnection(this, this)

    override fun onServiceStatusChanged(status: Status) {
        qsTile?.apply {
            state =
                when (status) {
                    // Starting/Stopping stay ACTIVE/INACTIVE (not UNAVAILABLE) so the tile is
                    // never greyed-out and un-tappable mid-transition — the user can always toggle.
                    Status.Started, Status.Starting -> Tile.STATE_ACTIVE
                    else -> Tile.STATE_INACTIVE
                }
            // Small state caption under the spider, Russian to match the app. Tile.subtitle lands on
            // Android 11+ (API 30); guard at R — never lower, or older devices hit NoSuchMethodError.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                subtitle =
                    when (status) {
                        Status.Started -> "Подключено"
                        Status.Starting -> "Подключение…"
                        Status.Stopping -> "Отключение…"
                        else -> "Отключено"
                    }
            }
            updateTile()
        }
    }

    override fun onStartListening() {
        super.onStartListening()
        connection.connect()
    }

    override fun onStopListening() {
        connection.disconnect()
        super.onStopListening()
    }

    override fun onClick() {
        val keyguardManager = getSystemService(Context.KEYGUARD_SERVICE) as KeyguardManager
        if (keyguardManager.isKeyguardLocked) {
            unlockAndRun {
                toggleService()
            }
        } else {
            toggleService()
        }
    }

    private fun toggleService() {
        when (connection.status) {
            Status.Stopped -> BoxService.start()
            Status.Started -> BoxService.stop()
            else -> {}
        }
    }
}

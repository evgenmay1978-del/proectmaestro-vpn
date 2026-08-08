package com.maestrovpn.tv.compose.screen.tvhome

import android.animation.ValueAnimator
import android.content.Context
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import android.os.Build
import android.view.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.LocalView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import kotlin.math.PI
import kotlin.math.abs

@Composable
internal fun rememberMobile4DTilt(enabled: Boolean = true): Mobile4DTiltVector {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val view = LocalView.current
    var systemAnimationsEnabled by remember {
        mutableStateOf(mobile4DSystemAnimationsEnabled())
    }
    DisposableEffect(lifecycleOwner) {
        val animationObserver = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                systemAnimationsEnabled = mobile4DSystemAnimationsEnabled()
            }
        }
        lifecycleOwner.lifecycle.addObserver(animationObserver)
        onDispose { lifecycleOwner.lifecycle.removeObserver(animationObserver) }
    }
    var tilt by remember { mutableStateOf(Mobile4DTiltVector.Zero) }

    DisposableEffect(context, lifecycleOwner, view, enabled, systemAnimationsEnabled) {
        if (!enabled || !systemAnimationsEnabled) {
            tilt = Mobile4DTiltVector.Zero
            return@DisposableEffect onDispose { }
        }

        val sensorManager = context.getSystemService(Context.SENSOR_SERVICE) as? SensorManager
        val sensor = sensorManager?.getDefaultSensor(Sensor.TYPE_GAME_ROTATION_VECTOR)
            ?: sensorManager?.getDefaultSensor(Sensor.TYPE_ROTATION_VECTOR)
        if (sensorManager == null || sensor == null) {
            tilt = Mobile4DTiltVector.Zero
            return@DisposableEffect onDispose { }
        }

        val adapter = Mobile4DTiltSensorAdapter(
            displayRotation = { mobile4DDisplayRotation(view.display?.rotation ?: Surface.ROTATION_0) },
            onTilt = { tilt = it },
        )
        var registered = false
        fun register() {
            if (registered) return
            if (!mobile4DSystemAnimationsEnabled()) {
                tilt = Mobile4DTiltVector.Zero
                return
            }
            adapter.reset()
            tilt = Mobile4DTiltVector.Zero
            registered = sensorManager.registerListener(adapter, sensor, SensorManager.SENSOR_DELAY_GAME)
            if (!registered) tilt = Mobile4DTiltVector.Zero
        }
        fun unregister() {
            if (registered) sensorManager.unregisterListener(adapter)
            registered = false
            adapter.reset()
            tilt = Mobile4DTiltVector.Zero
        }

        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> register()
                Lifecycle.Event.ON_PAUSE,
                Lifecycle.Event.ON_STOP,
                Lifecycle.Event.ON_DESTROY -> unregister()
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        if (lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) register()

        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
            unregister()
        }
    }

    return if (enabled && systemAnimationsEnabled) tilt else Mobile4DTiltVector.Zero
}

private class Mobile4DTiltSensorAdapter(
    private val displayRotation: () -> Mobile4DDisplayRotation,
    private val onTilt: (Mobile4DTiltVector) -> Unit,
) : SensorEventListener {
    private val rotationMatrix = FloatArray(9)
    private val orientation = FloatArray(3)
    private val calibrator = Mobile4DNeutralCalibrator()
    private var filtered = Mobile4DTiltVector.Zero
    private var lastTimestampNanos = 0L
    private var lastDisplayRotation: Mobile4DDisplayRotation? = null

    override fun onSensorChanged(event: SensorEvent) {
        val currentDisplayRotation = displayRotation()
        if (lastDisplayRotation != currentDisplayRotation) {
            reset(currentDisplayRotation)
        }

        SensorManager.getRotationMatrixFromVector(rotationMatrix, event.values)
        SensorManager.getOrientation(rotationMatrix, orientation)
        val portraitDegrees = Mobile4DTiltVector(
            x = orientation[2].radiansToDegrees(),
            y = orientation[1].radiansToDegrees(),
        )
        val displayDegrees = mobile4DRemapForDisplayRotation(portraitDegrees, currentDisplayRotation)
        val calibratedDegrees = calibrator.accept(displayDegrees, event.timestamp) ?: run {
            onTilt(Mobile4DTiltVector.Zero)
            return
        }
        val target = Mobile4DTiltVector(
            x = mobile4DNormalizeTiltDegrees(calibratedDegrees.x),
            y = mobile4DNormalizeTiltDegrees(calibratedDegrees.y),
        )
        val elapsedMillis = if (lastTimestampNanos == 0L) {
            0L
        } else {
            ((event.timestamp - lastTimestampNanos).coerceAtLeast(0L) / NANOS_PER_MILLISECOND)
        }
        filtered = if (lastTimestampNanos == 0L) target else mobile4DLowPass(filtered, target, elapsedMillis)
        lastTimestampNanos = event.timestamp
        onTilt(filtered)
    }

    override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) = Unit

    fun reset(displayRotation: Mobile4DDisplayRotation? = null) {
        lastDisplayRotation = displayRotation
        lastTimestampNanos = 0L
        filtered = Mobile4DTiltVector.Zero
        calibrator.reset()
    }
}

private class Mobile4DNeutralCalibrator {
    private var candidate: Mobile4DTiltVector? = null
    private var candidateSinceNanos = 0L
    private var neutral: Mobile4DTiltVector? = null

    fun accept(sample: Mobile4DTiltVector, timestampNanos: Long): Mobile4DTiltVector? {
        val calibratedNeutral = neutral
        if (calibratedNeutral != null) {
            return Mobile4DTiltVector(
                x = sample.x - calibratedNeutral.x,
                y = sample.y - calibratedNeutral.y,
            )
        }

        val stableCandidate = candidate
        if (
            stableCandidate == null ||
            abs(sample.x - stableCandidate.x) > CALIBRATION_STABILITY_DEGREES ||
            abs(sample.y - stableCandidate.y) > CALIBRATION_STABILITY_DEGREES
        ) {
            candidate = sample
            candidateSinceNanos = timestampNanos
            return null
        }
        if (timestampNanos - candidateSinceNanos < CALIBRATION_STABLE_NANOS) return null

        neutral = stableCandidate
        return Mobile4DTiltVector.Zero
    }

    fun reset() {
        candidate = null
        candidateSinceNanos = 0L
        neutral = null
    }
}

private fun mobile4DDisplayRotation(surfaceRotation: Int): Mobile4DDisplayRotation = when (surfaceRotation) {
    Surface.ROTATION_90 -> Mobile4DDisplayRotation.Rotation90
    Surface.ROTATION_180 -> Mobile4DDisplayRotation.Rotation180
    Surface.ROTATION_270 -> Mobile4DDisplayRotation.Rotation270
    else -> Mobile4DDisplayRotation.Rotation0
}

private fun Float.radiansToDegrees(): Float = this * (180f / PI.toFloat())

private fun mobile4DSystemAnimationsEnabled(): Boolean =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ValueAnimator.areAnimatorsEnabled() else true

private const val CALIBRATION_STABILITY_DEGREES = 0.75f
private const val CALIBRATION_STABLE_NANOS = 250_000_000L
private const val NANOS_PER_MILLISECOND = 1_000_000L

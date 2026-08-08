package com.maestrovpn.tv.compose.screen.tvhome

import android.animation.ValueAnimator
import android.app.ActivityManager
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.RememberObserver
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.platform.LocalContext
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import kotlin.coroutines.coroutineContext

internal data class Mobile4DLoadedPage(
    val descriptor: Mobile4DAssetPage,
    val bitmap: Bitmap,
)

/**
 * Immutable draw lease. A bitmap is recycled only after the store and every committed
 * composition have released their leases, so a superseded frame cannot draw a recycled page.
 */
internal class Mobile4DBitmapLease private constructor(
    private val pageReferences: Map<Mobile4DAssetLight, List<Mobile4DBitmapReference>>,
) : AutoCloseable, RememberObserver {
    private var closed = false
    private val allReferences = pageReferences.values.flatten()

    val decodedByteCount: Long = mobile4DWithRetainedReferences(
        references = allReferences,
        retain = Mobile4DBitmapReference::retain,
        release = Mobile4DBitmapReference::release,
    ) {
        allReferences.distinct().sumOf { it.bitmap.allocationByteCount.toLong() }
    }

    fun pages(light: Mobile4DAssetLight): List<Mobile4DLoadedPage> =
        pageReferences[light].orEmpty().map { Mobile4DLoadedPage(it.descriptor, it.bitmap) }

    fun hasLight(light: Mobile4DAssetLight): Boolean = pageReferences[light].orEmpty().isNotEmpty()

    internal fun fork(): Mobile4DBitmapLease = Mobile4DBitmapLease(pageReferences)

    @Synchronized
    override fun close() {
        if (closed) return
        closed = true
        for (index in allReferences.indices.reversed()) allReferences[index].release()
    }

    override fun onRemembered() = Unit

    override fun onForgotten() = close()

    override fun onAbandoned() = close()

    companion object {
        internal fun create(
            pageReferences: Map<Mobile4DAssetLight, List<Mobile4DBitmapReference>>,
        ): Mobile4DBitmapLease = Mobile4DBitmapLease(pageReferences)

        internal fun empty(): Mobile4DBitmapLease = Mobile4DBitmapLease(emptyMap())
    }
}

internal class Mobile4DBitmapReference(
    val descriptor: Mobile4DAssetPage,
    val bitmap: Bitmap,
) {
    private var leaseCount = 0

    @Synchronized
    fun retain() {
        check(!bitmap.isRecycled) { "Cannot retain a recycled mobile 4D atlas page" }
        leaseCount += 1
    }

    @Synchronized
    fun release() {
        check(leaseCount > 0) { "Unbalanced mobile 4D bitmap lease" }
        leaseCount -= 1
        if (leaseCount == 0 && !bitmap.isRecycled) bitmap.recycle()
    }

    @Synchronized
    fun recycleIfUnowned() {
        if (leaseCount == 0 && !bitmap.isRecycled) bitmap.recycle()
    }
}

internal class Mobile4DBitmapStore(
    context: Context,
    val policy: Mobile4DAssetMemoryPolicy,
) : AutoCloseable {
    private val context = context.applicationContext
    private var storeLease = Mobile4DBitmapLease.empty()
    private var centrePages: List<Mobile4DBitmapReference>? = null
    private var visiblePageReferences: Map<Mobile4DAssetLight, List<Mobile4DBitmapReference>> = emptyMap()
    private var pendingReleasePages: List<Mobile4DBitmapReference> = emptyList()
    private var visibleLights: Set<Mobile4DAssetLight> = emptySet()
    private var effectiveRetention = policy.retention
    private var activeSide = Mobile4DLightSide.Right
    private val generationState = mutableLongStateOf(0L)
    internal val generation: State<Long> = generationState
    private var closed = false

    internal fun acquireCurrent(): Mobile4DBitmapLease = storeLease.fork()

    suspend fun ensureForTilt(tiltX: Float): Boolean {
        check(!closed) { "Mobile4DBitmapStore is closed" }
        if (policy.retention == Mobile4DAssetRetention.None) return true

        return try {
            ensureForTiltInternal(tiltX)
        } catch (_: OutOfMemoryError) {
            effectiveRetention = if (centrePages == null) {
                Mobile4DAssetRetention.None
            } else {
                publishCentreOnly()
                Mobile4DAssetRetention.CentreOnly
            }
            true
        } catch (_: IOException) {
            effectiveRetention = if (centrePages == null) {
                Mobile4DAssetRetention.None
            } else {
                publishCentreOnly()
                Mobile4DAssetRetention.CentreOnly
            }
            true
        }
    }

    private suspend fun ensureForTiltInternal(tiltX: Float): Boolean {
        if (effectiveRetention == Mobile4DAssetRetention.None) return true
        val requestedSide = mobile4DActiveLightSide(tiltX, activeSide)
        if (centrePages == null) {
            val decodedCentre = decodeLight(Mobile4DAssetLight.Centre)
            var centrePublished = false
            try {
                if (
                    !mobile4DAllocationsFitBudget(
                        allocations = decodedCentre,
                        budgetBytes = policy.decodedArtBudgetBytes,
                        allocationByteCount = { it.bitmap.allocationByteCount.toLong() },
                    )
                ) {
                    effectiveRetention = Mobile4DAssetRetention.None
                    return true
                }
                centrePages = decodedCentre
                publish(mapOf(Mobile4DAssetLight.Centre to decodedCentre))
                centrePublished = true
            } finally {
                if (!centrePublished) {
                    if (centrePages === decodedCentre) centrePages = null
                    decodedCentre.recycleUnowned()
                }
            }
        }
        if (effectiveRetention == Mobile4DAssetRetention.CentreOnly) return true

        val requestedAssetLight = requestedSide.toAssetLight()
        if (effectiveRetention == Mobile4DAssetRetention.AllLights && visibleLights.size < 3) {
            loadAllLights(activeFirst = requestedAssetLight)
            activeSide = requestedSide
            return true
        }
        if (requestedAssetLight in visibleLights) {
            activeSide = requestedSide
            return true
        }

        return switchConstrainedSide(requestedSide, requestedAssetLight)
    }

    override fun close() {
        if (closed) return
        closed = true
        storeLease.close()
        storeLease = Mobile4DBitmapLease.empty()
        centrePages = null
        visibleLights = emptySet()
        visiblePageReferences = emptyMap()
        pendingReleasePages = emptyList()
        generationState.longValue += 1L
    }

    private suspend fun loadAllLights(activeFirst: Mobile4DAssetLight) {
        val centre = requireNotNull(centrePages)
        val first = decodeLight(activeFirst)
        val centreAndFirst = mapOf(
            Mobile4DAssetLight.Centre to centre,
            activeFirst to first,
        )
        if (centreAndFirst.decodedByteCount() > policy.decodedArtBudgetBytes) {
            first.recycleUnowned()
            effectiveRetention = Mobile4DAssetRetention.CentreOnly
            return
        }
        publish(centreAndFirst)

        val other = if (activeFirst == Mobile4DAssetLight.Left) {
            Mobile4DAssetLight.Right
        } else {
            Mobile4DAssetLight.Left
        }
        val second = decodeLight(other)
        val all = centreAndFirst + (other to second)
        if (all.decodedByteCount() <= policy.decodedArtBudgetBytes) {
            publish(all)
        } else {
            second.recycleUnowned()
            effectiveRetention = Mobile4DAssetRetention.CentreAndActiveSide
        }
    }

    private suspend fun switchConstrainedSide(
        requestedSide: Mobile4DLightSide,
        requestedAssetLight: Mobile4DAssetLight,
    ): Boolean {
        val centre = requireNotNull(centrePages)
        if (pendingReleasePages.isEmpty()) {
            pendingReleasePages = visiblePageReferences
                .filterKeys { it != Mobile4DAssetLight.Centre }
                .values
                .flatten()
            publish(mapOf(Mobile4DAssetLight.Centre to centre))
        }

        // Wait until Compose commits its centre-only lease. Reference-counted old pages recycle
        // at that point; allocating the new side earlier would exceed a two-light budget. A
        // bounded wait returns false, and the collector retries deterministically while the same
        // side remains requested, rather than pinning the scene at centre-only.
        var waitedFrames = 0
        while (pendingReleasePages.any { !it.bitmap.isRecycled } && waitedFrames < MAX_RELEASE_WAIT_FRAMES) {
            withFrameNanos { }
            waitedFrames += 1
        }
        coroutineContext.ensureActive()
        if (pendingReleasePages.any { !it.bitmap.isRecycled }) return false
        pendingReleasePages = emptyList()

        val side = decodeLight(requestedAssetLight)
        val centreAndSide = mapOf(
            Mobile4DAssetLight.Centre to centre,
            requestedAssetLight to side,
        )
        if (centreAndSide.decodedByteCount() <= policy.decodedArtBudgetBytes) {
            publish(centreAndSide)
            activeSide = requestedSide
        } else {
            side.recycleUnowned()
            effectiveRetention = Mobile4DAssetRetention.CentreOnly
        }
        return true
    }

    private suspend fun decodeLight(light: Mobile4DAssetLight): List<Mobile4DBitmapReference> {
        val owned = Mobile4DOwnedBatch<Mobile4DBitmapReference> { it.recycleIfUnowned() }
        try {
            withContext(Dispatchers.IO) {
                Mobile4DGeneratedAssets.pages
                    .asSequence()
                    .filter { it.light == light }
                    .sortedBy { it.pageIndex }
                    .forEach { descriptor ->
                        coroutineContext.ensureActive()
                        val options = BitmapFactory.Options().apply {
                            inPreferredConfig = Bitmap.Config.ARGB_8888
                            inScaled = true
                            inDensity = Mobile4DGeneratedAssets.masterWidth
                            inTargetDensity = policy.targetWidthPx
                        }
                        val bitmap = context.assets.open(descriptor.path).use { input ->
                            BitmapFactory.decodeStream(input, null, options)
                        } ?: throw IOException("Cannot decode mobile 4D atlas page ${descriptor.path}")
                        var added = false
                        try {
                            owned.add(Mobile4DBitmapReference(descriptor, bitmap))
                            added = true
                        } finally {
                            if (!added && !bitmap.isRecycled) bitmap.recycle()
                        }
                    }
            }
            // withContext has prompt-cancellation semantics. This explicit check plus the outer
            // finally keeps ownership local if cancellation wins during the IO-to-main handoff.
            coroutineContext.ensureActive()
            return owned.handOff()
        } finally {
            owned.releaseUnlessHandedOff()
        }
    }

    private fun publish(pages: Map<Mobile4DAssetLight, List<Mobile4DBitmapReference>>) {
        if (closed) {
            pages.values.flatten().recycleUnowned()
            error("Cannot publish to a closed Mobile4DBitmapStore")
        }
        val next = Mobile4DBitmapLease.create(pages)
        val previous = storeLease
        storeLease = next
        visibleLights = pages.keys
        visiblePageReferences = pages
        generationState.longValue += 1L
        previous.close()
    }

    private fun publishCentreOnly() {
        val centre = centrePages ?: return
        publish(mapOf(Mobile4DAssetLight.Centre to centre))
    }
}

@Composable
internal fun rememberMobile4DBitmaps(
    viewportWidthPx: Int,
    tiltX: Float,
    sceneMode: Mobile4DSceneMode = Mobile4DSceneMode.Home,
): Mobile4DBitmapLease {
    val context = LocalContext.current.applicationContext
    val activityManager = remember(context) {
        context.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager
    }
    val relightingEnabled = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        ValueAnimator.areAnimatorsEnabled()
    } else {
        true
    }
    val policy = remember(viewportWidthPx, sceneMode, relightingEnabled, activityManager) {
        mobile4DAssetMemoryPolicy(
            viewportWidthPx = viewportWidthPx,
            memoryClassMiB = activityManager?.memoryClass ?: DEFAULT_MEMORY_CLASS_MIB,
            isLowRamDevice = activityManager?.isLowRamDevice ?: true,
            relightingEnabled = relightingEnabled,
            sceneMode = sceneMode,
        )
    }
    val store = remember(context, policy) { Mobile4DBitmapStore(context, policy) }
    val currentTilt by rememberUpdatedState(tiltX)

    LaunchedEffect(store) {
        var side = Mobile4DLightSide.Right
        snapshotFlow { currentTilt }
            .map { value -> mobile4DActiveLightSide(value, side).also { side = it } }
            .distinctUntilChanged()
            .collectLatest { requestedSide ->
                val normalizedTilt = if (requestedSide == Mobile4DLightSide.Left) -1f else 1f
                while (!store.ensureForTilt(normalizedTilt)) delay(RELEASE_RETRY_DELAY_MILLIS)
            }
    }
    DisposableEffect(store) {
        onDispose(store::close)
    }

    return rememberMobile4DBitmapLease(store)
}

@Composable
private fun rememberMobile4DBitmapLease(store: Mobile4DBitmapStore): Mobile4DBitmapLease {
    val generation by store.generation
    return remember(store, generation) { store.acquireCurrent() }
}

private fun Mobile4DLightSide.toAssetLight(): Mobile4DAssetLight = when (this) {
    Mobile4DLightSide.Left -> Mobile4DAssetLight.Left
    Mobile4DLightSide.Right -> Mobile4DAssetLight.Right
}

private fun Map<Mobile4DAssetLight, List<Mobile4DBitmapReference>>.decodedByteCount(): Long =
    values.flatten().distinct().sumOf { it.bitmap.allocationByteCount.toLong() }

private fun List<Mobile4DBitmapReference>.recycleUnowned() {
    forEach(Mobile4DBitmapReference::recycleIfUnowned)
}

private const val DEFAULT_MEMORY_CLASS_MIB = 128
private const val MAX_RELEASE_WAIT_FRAMES = 3
private const val RELEASE_RETRY_DELAY_MILLIS = 32L

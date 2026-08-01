package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.zIndex
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.isSupported
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.premium.PremiumGold
import com.maestrovpn.tv.compose.premium.PremiumWalnut
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import kotlin.math.roundToInt

internal data class Mobile4DHomeReliefLayer(
    val name: String,
    val parallax: Mobile4DParallaxLayer,
)

/** Единственный runtime-план слоёв Home; тест сверяет его с generated manifest. */
internal val mobile4DHomeReliefLayers = listOf(
    Mobile4DHomeReliefLayer("console", Mobile4DParallaxLayer.Console),
    Mobile4DHomeReliefLayer("contacts", Mobile4DParallaxLayer.Console),
    Mobile4DHomeReliefLayer("frame", Mobile4DParallaxLayer.Frame),
    Mobile4DHomeReliefLayer("cartouche", Mobile4DParallaxLayer.Cartouche),
    Mobile4DHomeReliefLayer("vines", Mobile4DParallaxLayer.Vines),
    Mobile4DHomeReliefLayer("arc", Mobile4DParallaxLayer.Arc),
)
internal val mobile4DHomeLayerOrder =
    listOf("wood") + mobile4DHomeReliefLayers.map(Mobile4DHomeReliefLayer::name) + "ring"

/** Clean phone-only compositor. It never draws the obsolete flattened mobile scene. */
@Composable
internal fun Mobile4DHome(
    statusText: String,
    connected: Boolean,
    connecting: Boolean,
    protocols: List<String>,
    selected: String?,
    activeProtocol: String? = null,
    accountLogin: String? = null,
    daysLeft: Int? = null,
    accountExpires: String? = null,
    hasSubProfile: Boolean = false,
    hasOlcrtcCreds: Boolean = false,
    olcrtcProvider: String? = null,
    onToggleConnect: () -> Unit,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit = {},
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit = {},
    onShareIos: () -> Unit = {},
    onScanQr: () -> Unit = {},
    onEnterTrial: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    BoxWithConstraints(
        modifier = modifier
            .fillMaxSize()
            .clipToBounds()
            .testTag("premium-phone-home"),
    ) {
        val density = LocalDensity.current
        val viewportWidthPx = with(density) { maxWidth.roundToPx() }.coerceAtLeast(1)
        val viewportHeightPx = with(density) { maxHeight.roundToPx() }.coerceAtLeast(1)
        val layout = remember(viewportWidthPx, viewportHeightPx) {
            mobile4DSceneLayout(viewportWidthPx.toFloat(), viewportHeightPx.toFloat())
        }
        val reference = phoneHomeReferenceLayout(maxWidth.value, maxHeight.value)
        Mobile4DSceneAndHero(
            layout = layout,
            reference = reference,
            viewportWidthPx = viewportWidthPx,
            connected = connected,
            connecting = connecting,
            onToggleConnect = onToggleConnect,
            modifier = Modifier.fillMaxSize(),
        )

        // Дека сама знает свои границы по эталону владельца (phoneHomeReferenceLayout) и
        // сама держит верхний отступ, поэтому здесь нет ни padding от медальона, ни
        // «нижней доли экрана»: одна область — один владелец отрисовки.
        PhoneHomeControlDeck(
            statusText = statusText,
            connected = connected,
            connecting = connecting,
            activeProtocol = activeProtocol,
            accountLogin = accountLogin,
            daysLeft = daysLeft,
            accountExpires = accountExpires,
            protocols = protocols,
            selected = selected,
            hasSubProfile = hasSubProfile,
            hasOlcrtcCreds = hasOlcrtcCreds,
            olcrtcProvider = olcrtcProvider,
            onSelectProtocol = onSelectProtocol,
            onSelectOlcrtc = onSelectOlcrtc,
            onBuy = onBuy,
            onEnterCode = onEnterCode,
            onSplitTunnel = onSplitTunnel,
            onShareIos = onShareIos,
            onScanQr = onScanQr,
            onEnterTrial = onEnterTrial,
            modifier = Modifier
                .zIndex(3f)
                .fillMaxSize(),
        )
    }
}

@Composable
private fun Mobile4DSceneAndHero(
    layout: Mobile4DSceneLayout,
    reference: PhoneHomeReferenceLayout,
    viewportWidthPx: Int,
    connected: Boolean,
    connecting: Boolean,
    onToggleConnect: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val density = LocalDensity.current
    val tilt = rememberMobile4DTilt()
    val lease = rememberMobile4DBitmaps(
        viewportWidthPx = viewportWidthPx,
        tiltX = tilt.x,
        sceneMode = Mobile4DSceneMode.Home,
    )
    val pages = remember(lease) { LoadedAtlasPages.from(lease) }
    val requestedSide = if (tilt.x < 0f) Mobile4DLightSide.Left else Mobile4DLightSide.Right
    val requestedSideAsset = requestedSide.toAssetLight()
    val lightMix = if (pages.has(requestedSideAsset)) {
        mobile4DLightMix(tilt.x, requestedSide)
    } else {
        Mobile4DLightMix(requestedSide, centerWeight = 1f, sideWeight = 0f)
    }

    // Медальон эталона стоит выше, чем кольцо на мастер-холсте, поэтому кольцо и глаз
    // получают ОДИН общий статический перенос и остаются совмещёнными. Дерево, рама,
    // картуш и лозы остаются на исходном кропе: лозы — полноэкранный слой, и сдвиг с
    // медальоном оголил бы низ и обрезал бока.
    val heroShift = Offset(0f, with(density) { reference.heroTranslationY.dp.toPx() })

    Box(modifier = modifier) {
        Canvas(Modifier.fillMaxSize()) {
            drawRect(PremiumWalnut)
            drawAtlasLayer(
                layer = "wood",
                parallaxLayer = Mobile4DParallaxLayer.Wood,
                layout = layout,
                tilt = tilt,
                lightMix = lightMix,
                pages = pages,
                opaque = true,
            )
            mobile4DHomeReliefLayers.forEach { layer ->
                drawReliefWithShadow(layer.name, layer.parallax, layout, tilt, lightMix, pages)
            }
            drawReliefWithShadow(
                "ring",
                Mobile4DParallaxLayer.RingAndEye,
                layout,
                tilt,
                lightMix,
                pages,
                heroShift,
            )
        }

        val ringEyeOffset = mobile4DParallaxOffset(
            Mobile4DParallaxLayer.RingAndEye,
            tilt.x,
            tilt.y,
        )
        val eyeOffsetXPx = with(density) { ringEyeOffset.xDp.dp.toPx() } + heroShift.x
        val eyeOffsetYPx = with(density) { ringEyeOffset.yDp.dp.toPx() } + heroShift.y
        val eyeLeftPx = (layout.medallionCenterX - layout.medallionRadiusX + eyeOffsetXPx).roundToInt()
        val eyeTopPx = (layout.medallionCenterY - layout.medallionRadiusY + eyeOffsetYPx).roundToInt()
        val eyeRightPx = (layout.medallionCenterX + layout.medallionRadiusX + eyeOffsetXPx).roundToInt()
        val eyeBottomPx = (layout.medallionCenterY + layout.medallionRadiusY + eyeOffsetYPx).roundToInt()
        var touchGaze by remember { mutableStateOf<Offset?>(null) }
        val eyeState = mobile4DEyeState(connected = connected, connecting = connecting)
        val eyeStateDescription = when (eyeState) {
            Mobile4DEyeState.Disconnected -> "Глаз закрыт"
            Mobile4DEyeState.Connecting -> "Глаз полуоткрыт"
            Mobile4DEyeState.Connected -> "Глаз открыт"
        }
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .zIndex(1f)
                .offset { IntOffset(eyeLeftPx, eyeTopPx) }
                .size(
                    width = with(density) { (eyeRightPx - eyeLeftPx).toDp() },
                    height = with(density) { (eyeBottomPx - eyeTopPx).toDp() },
                )
                .pointerInput(Unit) {
                    try {
                        awaitEachGesture {
                            val down = awaitFirstDown(requireUnconsumed = false)
                            fun track(position: Offset) {
                                val width = size.width.toFloat().coerceAtLeast(1f)
                                val height = size.height.toFloat().coerceAtLeast(1f)
                                touchGaze = Offset(
                                    x = (position.x / width - 0.5f) * 2f,
                                    y = (position.y / height - 0.5f) * 2f,
                                )
                            }
                            track(down.position)
                            while (true) {
                                val event = awaitPointerEvent()
                                val pressed = event.changes.firstOrNull { it.pressed } ?: break
                                track(pressed.position)
                            }
                            touchGaze = null
                        }
                    } finally {
                        touchGaze = null
                    }
                },
        ) {
            LivingEyeMedallion(
                connected = connected,
                touchGaze = touchGaze,
                opennessOverride = when (eyeState) {
                    Mobile4DEyeState.Disconnected -> 0f
                    Mobile4DEyeState.Connecting -> 0.5f
                    Mobile4DEyeState.Connected -> null
                },
                modifier = Modifier.fillMaxSize(),
            )
            Button(
                onClick = onToggleConnect,
                shape = CircleShape,
                contentPadding = PaddingValues(0.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color.Transparent,
                    contentColor = Color.White,
                ),
                elevation = ButtonDefaults.buttonElevation(
                    defaultElevation = 0.dp,
                    pressedElevation = 0.dp,
                ),
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("premium-eye")
                    .semantics {
                        contentDescription = if (connected) "Отключить VPN" else "Подключить VPN"
                        stateDescription = eyeStateDescription
                        role = Role.Button
                    },
                content = {},
            )
        }

        val cartoucheOffset = mobile4DParallaxOffset(
            Mobile4DParallaxLayer.Cartouche,
            tilt.x,
            tilt.y,
        )
        // Титул стоит по измеренным границам эталона, а не по прямоугольнику мастер-холста:
        // на мастере картуш шире и выше, и «MaestroVPN» уезжал вверх от резной таблички.
        // Параллакс картуша сохранён — надпись должна ехать вместе со своей табличкой.
        val titleLeftPx = with(density) { (reference.title.left.dp + cartoucheOffset.xDp.dp).toPx() }
        val titleTopPx = with(density) { (reference.title.top.dp + cartoucheOffset.yDp.dp).toPx() }
        val titleRightPx = with(density) { (reference.title.right.dp + cartoucheOffset.xDp.dp).toPx() }
        val titleBottomPx = with(density) { (reference.title.bottom.dp + cartoucheOffset.yDp.dp).toPx() }
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .zIndex(2f)
                .offset { IntOffset(titleLeftPx.roundToInt(), titleTopPx.roundToInt()) }
                .size(
                    width = with(density) { (titleRightPx - titleLeftPx).toDp() },
                    height = with(density) { (titleBottomPx - titleTopPx).toDp() },
                ),
        ) {
            // ⛔ Было 31 sp, потом 46 sp плоской заливкой — владелец: «надпись должна быть
            // объёмная, а не плоская». Замер эталона: строка ~242 dp при высоте глифов ~20 dp,
            // то есть средний кегль с большой разрядкой, а буквы вырезаны в металле: тёмная
            // прорезь снизу, светлая кромка сверху, градиент по грани.
            //
            // Три слоя одного и того же текста с ОДИНАКОВЫМИ constraints: `StepBased` —
            // детерминированная функция от текста, стиля-метрик и ширины, поэтому кегль у
            // всех трёх совпадает и буквы не разъезжаются. Цвет и кисть на метрики не влияют.
            val titleAutoSize = TextAutoSize.StepBased(
                minFontSize = 20.sp,
                maxFontSize = TITLE_MAX_FONT_SIZE,
                stepSize = 0.5.sp,
            )
            val titleBase = androidx.compose.ui.text.TextStyle(
                letterSpacing = TITLE_LETTER_SPACING,
                fontFamily = PlayfairFamily,
                fontWeight = FontWeight.Medium,
                textAlign = TextAlign.Center,
            )

            @Composable
            fun titleLayer(style: androidx.compose.ui.text.TextStyle, dy: Dp) {
                BasicText(
                    text = "MaestroVPN",
                    maxLines = 1,
                    autoSize = titleAutoSize,
                    style = style,
                    modifier = Modifier
                        .fillMaxWidth()
                        .offset(y = dy),
                )
            }

            // прорезь: тёмный оттиск на дно, ниже лицевой грани
            titleLayer(titleBase.copy(color = TITLE_CARVE), TITLE_CARVE_DEPTH)
            // кромка: слабый свет по верхнему краю буквы
            titleLayer(titleBase.copy(color = TITLE_RIM), -TITLE_RIM_LIFT)
            // лицевая грань: металл светлее сверху, темнее снизу
            titleLayer(
                titleBase.copy(
                    brush = Brush.verticalGradient(TITLE_FACE),
                    shadow = Shadow(
                        color = Color(0xC0000000),
                        offset = Offset(0f, with(density) { 1.dp.toPx() }),
                        blurRadius = with(density) { 2.dp.toPx() },
                    ),
                ),
                0.dp,
            )
        }
    }
}

private fun DrawScope.drawReliefWithShadow(
    layer: String,
    parallaxLayer: Mobile4DParallaxLayer,
    layout: Mobile4DSceneLayout,
    tilt: Mobile4DTiltVector,
    lightMix: Mobile4DLightMix,
    pages: LoadedAtlasPages,
    heroShift: Offset = Offset.Zero,
) {
    val parallax = mobile4DParallaxOffset(parallaxLayer, tilt.x, tilt.y)
    val parallaxPx = Offset(parallax.xDp * density, parallax.yDp * density) + heroShift
    val shadowTint = ColorFilter.tint(Color(0xFF110906), BlendMode.SrcIn)
    SHADOW_PASSES.forEach { pass ->
        drawAtlasFragments(
            layer = layer,
            light = Mobile4DAssetLight.Centre,
            layout = layout,
            pages = pages,
            offset = parallaxPx + Offset(pass.xDp * density, pass.yDp * density),
            alpha = pass.alpha,
            colorFilter = shadowTint,
        )
    }
    drawAtlasLayer(layer, parallaxLayer, layout, tilt, lightMix, pages, opaque = false, heroShift = heroShift)
}

private fun DrawScope.drawAtlasLayer(
    layer: String,
    parallaxLayer: Mobile4DParallaxLayer,
    layout: Mobile4DSceneLayout,
    tilt: Mobile4DTiltVector,
    lightMix: Mobile4DLightMix,
    pages: LoadedAtlasPages,
    opaque: Boolean,
    heroShift: Offset = Offset.Zero,
) {
    val parallax = mobile4DParallaxOffset(parallaxLayer, tilt.x, tilt.y)
    val offset = Offset(parallax.xDp * density, parallax.yDp * density) + heroShift
    val sideLight = lightMix.activeSide.toAssetLight()
    val sideAvailable = lightMix.sideWeight > 0f && pages.has(sideLight)
    if (opaque) {
        drawAtlasFragments(layer, Mobile4DAssetLight.Centre, layout, pages, offset, alpha = 1f)
        if (sideAvailable) {
            drawAtlasFragments(layer, sideLight, layout, pages, offset, alpha = lightMix.sideWeight)
        }
        return
    }

    if (!sideAvailable || !ADDITIVE_RELIEF_SUPPORTED) {
        drawAtlasFragments(layer, Mobile4DAssetLight.Centre, layout, pages, offset, alpha = 1f)
        return
    }

    val bounds = Mobile4DSceneAtlasIndex.boundsByLayer[layer]
        ?.map(layout, offset)
        ?.intersect(Rect(0f, 0f, size.width, size.height))
        ?: return
    if (bounds.isEmpty) return
    drawContext.canvas.saveLayer(bounds, Paint())
    try {
        drawAtlasFragments(
            layer,
            Mobile4DAssetLight.Centre,
            layout,
            pages,
            offset,
            alpha = lightMix.centerWeight,
            blendMode = BlendMode.Plus,
        )
        drawAtlasFragments(
            layer,
            sideLight,
            layout,
            pages,
            offset,
            alpha = lightMix.sideWeight,
            blendMode = BlendMode.Plus,
        )
    } finally {
        drawContext.canvas.restore()
    }
}

private fun DrawScope.drawAtlasFragments(
    layer: String,
    light: Mobile4DAssetLight,
    layout: Mobile4DSceneLayout,
    pages: LoadedAtlasPages,
    offset: Offset,
    alpha: Float,
    colorFilter: ColorFilter? = null,
    blendMode: BlendMode = BlendMode.SrcOver,
) {
    val fragments = Mobile4DSceneAtlasIndex.fragmentsByLightAndLayer[light]?.get(layer).orEmpty()
    fragments.forEach { fragment ->
        val page = pages[fragment.pagePath] ?: return@forEach
        val source = fragment.sourceRect
        val srcLeft = (source.x.toFloat() * page.image.width / page.descriptor.width).roundToInt()
        val srcTop = (source.y.toFloat() * page.image.height / page.descriptor.height).roundToInt()
        val srcRight = ((source.x + source.width).toFloat() * page.image.width / page.descriptor.width).roundToInt()
        val srcBottom = ((source.y + source.height).toFloat() * page.image.height / page.descriptor.height).roundToInt()

        val scene = fragment.sceneRect
        val dstLeft = (layout.mapMasterX(scene.x.toFloat()) + offset.x).roundToInt()
        val dstTop = (layout.mapMasterY(scene.y.toFloat()) + offset.y).roundToInt()
        val dstRight = (layout.mapMasterX((scene.x + scene.width).toFloat()) + offset.x).roundToInt()
        val dstBottom = (layout.mapMasterY((scene.y + scene.height).toFloat()) + offset.y).roundToInt()
        if (srcRight <= srcLeft || srcBottom <= srcTop || dstRight <= dstLeft || dstBottom <= dstTop) {
            return@forEach
        }
        drawImage(
            image = page.image,
            srcOffset = IntOffset(srcLeft, srcTop),
            srcSize = IntSize(srcRight - srcLeft, srcBottom - srcTop),
            dstOffset = IntOffset(dstLeft, dstTop),
            dstSize = IntSize(dstRight - dstLeft, dstBottom - dstTop),
            alpha = alpha.coerceIn(0f, 1f),
            colorFilter = colorFilter,
            blendMode = blendMode,
            filterQuality = FilterQuality.High,
        )
    }
}

private data class LoadedAtlasPage(
    val descriptor: Mobile4DAssetPage,
    val image: ImageBitmap,
)

private class LoadedAtlasPages private constructor(
    private val byPath: Map<String, LoadedAtlasPage>,
    private val lights: Set<Mobile4DAssetLight>,
) {
    operator fun get(path: String): LoadedAtlasPage? = byPath[path]

    fun has(light: Mobile4DAssetLight): Boolean = light in lights

    companion object {
        fun from(lease: Mobile4DBitmapLease): LoadedAtlasPages {
            val allLights = listOf(
                Mobile4DAssetLight.Left,
                Mobile4DAssetLight.Centre,
                Mobile4DAssetLight.Right,
            )
            val loaded = allLights.flatMap { light ->
                lease.pages(light).map { page ->
                    LoadedAtlasPage(page.descriptor, page.bitmap.asImageBitmap())
                }
            }
            return LoadedAtlasPages(
                byPath = loaded.associateBy { it.descriptor.path },
                lights = allLights.filterTo(mutableSetOf(), lease::hasLight),
            )
        }
    }
}

private object Mobile4DSceneAtlasIndex {
    val fragmentsByLightAndLayer: Map<Mobile4DAssetLight, Map<String, List<Mobile4DAssetFragment>>> =
        Mobile4DGeneratedAssets.fragments
            .groupBy(Mobile4DAssetFragment::light)
            .mapValues { (_, fragments) -> fragments.groupBy(Mobile4DAssetFragment::layer) }

    val boundsByLayer: Map<String, SceneBounds> = Mobile4DGeneratedAssets.fragments
        .filter { it.light == Mobile4DAssetLight.Centre }
        .groupBy(Mobile4DAssetFragment::layer)
        .mapValues { (_, fragments) -> SceneBounds.of(fragments) }
}

private data class SceneBounds(
    val left: Int,
    val top: Int,
    val right: Int,
    val bottom: Int,
) {
    fun map(layout: Mobile4DSceneLayout, offset: Offset): Rect = Rect(
        left = layout.mapMasterX(left.toFloat()) + offset.x,
        top = layout.mapMasterY(top.toFloat()) + offset.y,
        right = layout.mapMasterX(right.toFloat()) + offset.x,
        bottom = layout.mapMasterY(bottom.toFloat()) + offset.y,
    )

    companion object {
        fun of(fragments: List<Mobile4DAssetFragment>): SceneBounds = SceneBounds(
            left = fragments.minOf { it.sceneRect.x },
            top = fragments.minOf { it.sceneRect.y },
            right = fragments.maxOf { it.sceneRect.x + it.sceneRect.width },
            bottom = fragments.maxOf { it.sceneRect.y + it.sceneRect.height },
        )
    }
}

private data class ShadowPass(val xDp: Float, val yDp: Float, val alpha: Float)

private fun Mobile4DLightSide.toAssetLight(): Mobile4DAssetLight = when (this) {
    Mobile4DLightSide.Left -> Mobile4DAssetLight.Left
    Mobile4DLightSide.Right -> Mobile4DAssetLight.Right
}

private val ADDITIVE_RELIEF_SUPPORTED = BlendMode.Plus.isSupported()
/**
 * ⛔ Было 46 sp без разрядки — буквы получались на треть выше эталонных и почти касались краёв
 * таблички. Замер по эталону: строка занимает ~242 dp при высоте глифов ~20 dp, то есть набрана
 * НЕ крупным кеглем, а средним с большой разрядкой. Playfair 30 sp даёт 165 dp, недостающие
 * ~77 dp добираются трекингом по девяти промежуткам.
 */
private val TITLE_MAX_FONT_SIZE = 30.sp
private val TITLE_LETTER_SPACING = 8.sp
/**
 * Грань буквы: светлее сверху, темнее снизу — так металл читается объёмным. Средний тон набора
 * держится около замеренного на эталоне (186,170,137), крайние точки разведены от него.
 */
private val TITLE_FACE = listOf(Color(0xFFE4D6B4), Color(0xFF8C7A55))
/** Тёмная прорезь под гранью — дно вырезанной буквы. */
private val TITLE_CARVE = Color(0xFF241A10)
/** Слабая светлая кромка по верхнему краю. */
private val TITLE_RIM = Color(0x66FFF0C8)
private val TITLE_CARVE_DEPTH = 1.6.dp
private val TITLE_RIM_LIFT = 0.9.dp
private val SHADOW_PASSES = listOf(
    ShadowPass(0.6f, 1.0f, 0.16f),
    ShadowPass(1.2f, 2.1f, 0.10f),
    ShadowPass(2.0f, 3.2f, 0.06f),
)

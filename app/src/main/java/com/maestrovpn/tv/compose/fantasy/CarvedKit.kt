package com.maestrovpn.tv.compose.fantasy

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.ImageShader
import androidx.compose.ui.graphics.ShaderBrush
import androidx.compose.ui.graphics.TileMode
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R

/**
 * Резные ТВ-поверхности v4 — «материал мобильной версии» (owner 2026-07-12).
 * НИЧЕГО не растягивается (урок 2026-07-11 «малую картинку натянули, пикселит»):
 *
 *  • дерево — `tvm_wood_tile` (телефонный home_backdrop, ops/tv-mobile-kit.py) через
 *    [ImageShader] с [TileMode.Repeated]: родные пиксели 1:1 при любом размере;
 *  • рамка — процедурные градиент-штрихи в ЗОЛОТЕ телефонных рам (пипетка
 *    frame_bar/frame_button), чёткие на любой плотности;
 *  • CTA — тёмно-зелёный градиент + салатовая нижняя кромка (стиль «Купить подписку»).
 *
 * Фокус D-pad: золото теплеет к янтарю + чёткая золотая рамка внутри — БЕЗ blur-glow
 * и state-layer (правило ТВ-фокуса проекта).
 */
enum class CarvedStyle { Bark, Cta }

/** Плитка дерева как повторяющийся шейдер — родной масштаб, никакого растяжения.
 *  v4: тайл = ТЕЛЕФОННОЕ дерево (tvm_wood_tile из home_backdrop, ops/tv-mobile-kit.py) —
 *  «универсальный материал» owner 2026-07-12; кора эталона удалена вместе с carved_wood_tile. */
@Composable
fun rememberBarkBrush(): ShaderBrush {
    val bark = ImageBitmap.imageResource(R.drawable.tvm_wood_tile)
    return remember(bark) { ShaderBrush(ImageShader(bark, TileMode.Repeated, TileMode.Repeated)) }
}

// v4: рамка = ЗОЛОТО телефонных рам (пипетка frame_bar/frame_button — материал мобильной
// версии, ярче и желтее прежней бронзы эскиза); фокус-вариант теплеет к янтарю.
private val BronzeHi = Color(0xFFF0CD82)
private val BronzeMid = Color(0xFFA87C3A)
private val BronzeLow = Color(0xFF422C12)
private val AmberHi = Color(0xFFFFD9A0)
private val AmberMid = Color(0xFFC98F4A)
private val AmberLow = Color(0xFF6B4218)
private val EdgeDark = Color(0xFF050302)
private val InnerShade = Color(0xFF080503)
private val FocusGold = Color(0xFFFFCE8C)

// CTA-интерьер (пипетка с бара «Купить подписку» эскиза).
private val CtaTop = Color(0xFF090C03)
private val CtaBottom = Color(0xFF18240B)
private val CtaGlow = Color(0xFF3E5812)
private val CtaEdge = Color(0xFFAFC14E)

/**
 * Резная поверхность: интерьер (кора 1:1 или CTA-градиент) + процедурная бронза.
 * [focus] — лямбда 0..1 (анимируется снаружи), чтобы анимация жила только в draw-фазе.
 * [interiorDim] — доп. затемнение интерьера (поле ввода ставит больше — читаемость текста).
 * [selected] — постоянно тёплая янтарная бронза + лёгкий тёплый заливок интерьера
 * (выбранный протокол «Авто» на эскизе подсвечен янтарём; фокус-кольцо живёт отдельно).
 */
fun Modifier.carvedSurface(
    barkBrush: ShaderBrush?,
    focus: () -> Float,
    cornerRadius: Dp = 18.dp,
    style: CarvedStyle = CarvedStyle.Bark,
    interiorDim: Float = 0.10f,
    selected: Boolean = false,
): Modifier = drawWithContent {
    val f = focus().coerceIn(0f, 1f)
    val warm = maxOf(f, if (selected) 0.9f else 0f)
    val rad = cornerRadius.toPx()
    // 1) интерьер
    if (style == CarvedStyle.Bark && barkBrush != null) {
        drawRoundRect(brush = barkBrush, cornerRadius = CornerRadius(rad, rad))
        if (interiorDim > 0f) {
            drawRoundRect(color = Color.Black.copy(alpha = interiorDim), cornerRadius = CornerRadius(rad, rad))
        }
        if (selected) {
            // тёплый янтарный подсвет выбранной плиты (как амбер-лит «Авто» на эскизе)
            drawRoundRect(color = Color(0xFFB86A20).copy(alpha = 0.14f), cornerRadius = CornerRadius(rad, rad))
        }
    } else {
        drawRoundRect(
            brush = Brush.verticalGradient(listOf(CtaTop, CtaBottom)),
            cornerRadius = CornerRadius(rad, rad),
        )
        // салатовое свечение из нижней кромки (эскиз) — градиент, не blur
        drawRoundRect(
            brush = Brush.verticalGradient(
                0.55f to Color.Transparent,
                1f to CtaGlow.copy(alpha = 0.55f + 0.15f * f),
            ),
            cornerRadius = CornerRadius(rad, rad),
        )
        // тонкая салатовая внутренняя кромка по нижней половине
        clipRect(top = size.height * 0.55f) {
            val ins = 5.dp.toPx()
            drawRoundRect(
                color = CtaEdge.copy(alpha = 0.75f + 0.25f * f),
                topLeft = Offset(ins, ins),
                size = Size(size.width - 2 * ins, size.height - 2 * ins),
                cornerRadius = CornerRadius((rad - ins).coerceAtLeast(2f)),
                style = Stroke(width = 1.8.dp.toPx()),
            )
        }
    }
    // 1.5) объём интерьера (фото owner 2026-07-12 «нет глубины»): верхняя внутренняя тень
    // («козырёк» — свет сверху, плита утоплена) + тёплый нижний блик (отражение бронзы).
    // Градиенты, НЕ blur — дёшево и чисто на ТВ.
    drawRoundRect(
        brush = Brush.verticalGradient(
            0f to Color.Black.copy(alpha = 0.30f),
            0.30f to Color.Transparent,
        ),
        cornerRadius = CornerRadius(rad, rad),
    )
    drawRoundRect(
        brush = Brush.verticalGradient(
            0.86f to Color.Transparent,
            1f to Color(0xFFD8A860).copy(alpha = 0.10f),
        ),
        cornerRadius = CornerRadius(rad, rad),
    )
    // 2) бронза: тёмный контур → градиент-тело (теплеет при фокусе/выборе) → внутренняя тень
    strokeRounded(EdgeDark, 1.dp.toPx(), 2.dp.toPx(), rad)
    strokeRounded(
        Brush.verticalGradient(
            listOf(lerp(BronzeHi, AmberHi, warm), lerp(BronzeMid, AmberMid, warm), lerp(BronzeLow, AmberLow, warm)),
        ),
        3.dp.toPx(), 2.2.dp.toPx(), rad,
    )
    strokeRounded(InnerShade, 5.2.dp.toPx(), 1.2.dp.toPx(), rad)
    // 3) фокус: чёткая золотая рамка внутри (никаких blur-glow)
    if (f > 0.01f) {
        strokeRounded(FocusGold.copy(alpha = f), 7.5.dp.toPx(), 1.6.dp.toPx(), rad)
    }
    drawContent()
}

/** Штрих скруглённого контура с инсетом от края поверхности. */
private fun DrawScope.strokeRounded(color: Color, insetPx: Float, widthPx: Float, radPx: Float) {
    drawRoundRect(
        color = color,
        topLeft = Offset(insetPx, insetPx),
        size = Size(size.width - 2 * insetPx, size.height - 2 * insetPx),
        cornerRadius = CornerRadius((radPx - insetPx).coerceAtLeast(2f)),
        style = Stroke(width = widthPx),
    )
}

private fun DrawScope.strokeRounded(brush: Brush, insetPx: Float, widthPx: Float, radPx: Float) {
    drawRoundRect(
        brush = brush,
        topLeft = Offset(insetPx, insetPx),
        size = Size(size.width - 2 * insetPx, size.height - 2 * insetPx),
        cornerRadius = CornerRadius((radPx - insetPx).coerceAtLeast(2f)),
        style = Stroke(width = widthPx),
    )
}

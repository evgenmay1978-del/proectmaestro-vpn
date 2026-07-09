package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.NeonGreen
import kotlin.math.roundToInt

// Ноги паука из чистого ассета владельца (риг из rig_spider.py). В СТАТИЧНОЙ версии ноги
// не качаются (углы = 0) — паук сидит в своей естественной позе из арта.
private val SPIDER_LEG_RES = listOf(
    R.drawable.spider_leg_00, R.drawable.spider_leg_01, R.drawable.spider_leg_02,
    R.drawable.spider_leg_03, R.drawable.spider_leg_04, R.drawable.spider_leg_05,
    R.drawable.spider_leg_06, R.drawable.spider_leg_07, R.drawable.spider_leg_08,
)

// Глаза паука (тёплый янтарь) — доли медальона, сняты с spider_body.png.
private val EYE_COLOR = Color(0xFFFFB84D)
private val EYE_XS = listOf(0.44f, 0.56f)
private const val EYE_Y = 0.315f

/**
 * ТВ-медальон подключения — СТАРЫЙ вид (owner: «на андроид тв интерфейс старый без анимации»):
 * хром-кольцо + зелёная паутина (home_medallion_bg) и фотореал-паук из ассетов владельца, но
 * СТАТИЧНЫЙ — паук сидит по центру, ноги не шагают, глаза светятся ровно. Подключение/отключение —
 * один короткий кроссфейд (паук проявляется/тает, паутина насыщается), после чего экран не
 * перерисовывается вовсе (0 fps в покое — важно для 1 ГБ Sony/TCL). Убраны все вечные клоки старой
 * версии: gait/scramble (шаг ног), burst/pos (ползание), RingShine (блик по кольцу), пульс глаз.
 * D-pad-фокус и кнопка-toggle сохранены 1:1 (200dp, focusRequester, scale-отклик 140мс).
 */
@Composable
fun StaticTvMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    focusRequester: FocusRequester,
    modifier: Modifier = Modifier,
    sizeDp: Dp = 252.dp, // hero passes a height-aware size so tight TVs never squash the circle
) {
    // one-shot кроссфейды состояния (анимируются только в момент переключения, в покое держат значение)
    val power by animateFloatAsState(
        if (connected) 1f else 0.16f, tween(900, easing = FastOutSlowInEasing), label = "power",
    )
    val live by animateFloatAsState(
        if (connected) 1f else 0f, tween(700, easing = FastOutSlowInEasing), label = "live",
    )

    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val btnScale by animateFloatAsState(
        if (pressed) 0.94f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "medScale",
    )

    val bgP = painterResource(R.drawable.home_medallion_bg)
    val bodyBmp = ImageBitmap.imageResource(R.drawable.spider_body)
    val legBmps = SPIDER_LEG_RES.map { ImageBitmap.imageResource(it) }
    val webFilter = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0.12f + 0.88f * power) })

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.size(sizeDp).graphicsLayer { scaleX = btnScale; scaleY = btnScale },
    ) {
        // зелёное свечение вокруг медальона — ярче при подключении и фокусе (статика, draw-фаза)
        Box(
            Modifier.size(sizeDp).drawBehind {
                val c = Offset(size.width / 2f, size.height / 2f)
                val r = size.minDimension * 0.5f
                val a = 0.18f + 0.30f * power + if (focused) 0.26f else 0f
                drawCircle(
                    brush = Brush.radialGradient(
                        0.00f to Color.Transparent, 0.64f to Color.Transparent,
                        0.82f to NeonGreen.copy(alpha = a), 1.00f to Color.Transparent,
                        center = c, radius = r * 1.16f,
                    ),
                    radius = r * 1.16f, center = c,
                )
            },
        )

        Box(Modifier.size(sizeDp - 20.dp).clip(CircleShape)) {
            // 1) чистая кнопка владельца: хром-кольцо + зелёная web
            Image(bgP, null, Modifier.size(sizeDp - 20.dp), contentScale = ContentScale.Fit, colorFilter = webFilter)

            // 2) статичная зелёная кромка у внутреннего края кольца (насыщается с power)
            Box(
                Modifier.size(sizeDp - 20.dp).drawBehind {
                    val c = Offset(size.width / 2f, size.height / 2f)
                    val r = size.minDimension / 2f
                    drawCircle(
                        brush = Brush.radialGradient(
                            0.62f to Color.Transparent,
                            0.78f to NeonGreen.copy(alpha = 0.05f + 0.22f * power),
                            0.88f to Color.Transparent,
                            center = c, radius = r,
                        ),
                        radius = r, center = c,
                    )
                },
            )

            // 3) СТАТИЧНЫЙ паук: тени ног + ноги + тело в естественной позе арта, по центру.
            //    Проявляется кроссфейдом `live` при подключении; ничего не шагает и не ползёт.
            if (live > 0.01f) {
                Box(
                    Modifier.size(sizeDp - 20.dp).drawBehind {
                        val box = IntSize(size.width.roundToInt(), size.height.roundToInt())
                        val shadow = ColorFilter.tint(Color.Black)
                        legBmps.forEach { bmp ->
                            drawImage(bmp, dstOffset = IntOffset(6, 8), dstSize = box, alpha = 0.30f * live, colorFilter = shadow)
                        }
                        legBmps.forEach { bmp ->
                            drawImage(bmp, dstOffset = IntOffset(0, 0), dstSize = box, alpha = live)
                        }
                        drawImage(bodyBmp, dstOffset = IntOffset(6, 8), dstSize = box, alpha = 0.34f * live, colorFilter = shadow)
                        drawImage(bodyBmp, dstOffset = IntOffset(0, 0), dstSize = box, alpha = live)
                        // глаза — ровный тёплый свет (без пульса)
                        val eyeA = 0.62f * live
                        val er = size.width * 0.05f
                        EYE_XS.forEach { ex ->
                            val ec = Offset(ex * size.width, EYE_Y * size.height)
                            drawCircle(
                                brush = Brush.radialGradient(
                                    0f to EYE_COLOR.copy(alpha = eyeA),
                                    0.55f to EYE_COLOR.copy(alpha = eyeA * 0.45f),
                                    1f to Color.Transparent,
                                    center = ec, radius = er,
                                ),
                                radius = er, center = ec,
                            )
                        }
                    },
                )
            }

            // 4) затемнение когда выключено
            Box(Modifier.size(sizeDp - 20.dp).drawBehind { drawCircle(Color.Black.copy(alpha = (1f - power) * 0.30f)) })
        }

        Button(
            onClick = onToggle,
            shape = CircleShape,
            interactionSource = interaction,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier.size(sizeDp * (200f / 252f)).focusRequester(focusRequester),
            content = {},
        )
    }
}

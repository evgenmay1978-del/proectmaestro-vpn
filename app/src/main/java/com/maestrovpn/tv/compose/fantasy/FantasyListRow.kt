package com.maestrovpn.tv.compose.fantasy

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Dark-Fantasy list row — replaces Material3 `ListItem`/`Card` grouping on the settings-style
 * screens. A carved-wood row framed in aged bronze (`frame_bar` 9-patch); leading rune-icon
 * (emerald), a bold title + optional dim subtitle, and a trailing slot (chevron / switch / badge).
 *
 * All interaction stays a plain `onClick` so every caller's navigation / persistence survives.
 */
@Composable
fun FantasyListRow(
    title: String,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    subtitle: String? = null,
    onClick: (() -> Unit)? = null,
    iconTint: Color = NeonGreen,
    trailing: @Composable (() -> Unit)? = null,
) {
    // ТВ-фокус: чёткая рамка-акцент вместо дефолтного ripple/state-layer (белёсая вуаль
    // поверх дерева при D-pad, фото owner 2026-07-11); на телефоне фидбек = лёгкий пресс-скейл.
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(if (pressed) 0.98f else 1f, tween(120), label = "rowScale")
    val focusAlpha by animateFloatAsState(if (focused) 1f else 0f, tween(120), label = "rowFocus")
    // ТВ: резная строка (кора 1:1 + процедурная бронза). Прежний frame_bar (1042×348)
    // сплющивался в ~2.7 раза по высоте — орнаменты превращались в кашу (тарифы, фото owner).
    val isTv = com.maestrovpn.tv.compose.rememberIsTv()
    val tvShape = RoundedCornerShape(16.dp)
    Row(
        modifier = modifier
            .fillMaxWidth()
            .graphicsLayer {
                scaleX = if (isTv) 1f else scale
                scaleY = if (isTv) 1f else scale
            }
            .then(
                if (onClick != null) {
                    Modifier.clickable(interactionSource = interaction, indication = null, role = Role.Button) { onClick() }
                } else {
                    Modifier
                },
            )
            .then(
                if (isTv) {
                    Modifier
                        .clip(tvShape)
                        .background(if (focused) Color(0xFF14261B) else Color(0xFF171B1A))
                        .border(
                            width = if (focused) 3.dp else 1.dp,
                            color = if (focused) Color(0xFFE6BE76) else Color(0xFF353A37),
                            shape = tvShape,
                        )
                } else {
                    Modifier
                        .fantasyFrame(R.drawable.frame_bar)
                        .fantasyFocusFrame({ focusAlpha }, NeonGreen)
                },
            )
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Start,
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = iconTint, modifier = Modifier.size(24.dp))
            Spacer(Modifier.width(14.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(
                title,
                color = if (isTv) Color(0xFFF3F0E8) else Color(0xFFF1EEE6),
                fontWeight = FontWeight.SemiBold,
                fontSize = if (isTv) 17.sp else 16.sp,
            )
            if (!subtitle.isNullOrBlank()) {
                Text(
                    subtitle,
                    color = if (isTv) Color(0xFFA7AAA3) else GoldMid.copy(alpha = 0.8f),
                    fontSize = if (isTv) 13.sp else 12.5.sp,
                )
            }
        }
        if (trailing != null) {
            Spacer(Modifier.width(10.dp))
            trailing()
        }
    }
}

/**
 * Shared premium background: phone keeps oak_bg; TV uses the graphite premium scene.
 * A static key-light and vignette place every route in the same physical scene.
 */
@Composable
fun FantasyScreenBackground(
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    val isTv = com.maestrovpn.tv.compose.rememberIsTv()
    Box(modifier.fillMaxSize()) {
        if (isTv) {
            Box(
                Modifier
                    .matchParentSize()
                    .background(Color(0xFF070909))
                    .drawBehind {
                        drawCircle(
                            brush = Brush.radialGradient(
                                listOf(
                                    Color(0xFF51B56E).copy(alpha = 0.09f),
                                    Color(0xFFC9A15E).copy(alpha = 0.025f),
                                    Color.Transparent,
                                ),
                                center = Offset(size.width * 0.18f, size.height * 0.36f),
                                radius = size.maxDimension * 0.62f,
                            ),
                            center = Offset(size.width * 0.18f, size.height * 0.36f),
                            radius = size.maxDimension * 0.62f,
                        )
                        drawCircle(
                            brush = Brush.radialGradient(
                                listOf(Color(0xFFC9A15E).copy(alpha = 0.055f), Color.Transparent),
                                center = Offset(size.width * 0.82f, size.height * 0.42f),
                                radius = size.maxDimension * 0.58f,
                            ),
                            center = Offset(size.width * 0.82f, size.height * 0.42f),
                            radius = size.maxDimension * 0.58f,
                        )
                    },
            )
        } else {
            androidx.compose.foundation.Image(
                painter = painterResource(R.drawable.oak_bg),
                contentDescription = null,
                modifier = Modifier.matchParentSize(),
                contentScale = ContentScale.Crop,
            )
            // Preserve the phone lighting exactly.
            Box(
                Modifier
                    .matchParentSize()
                    .drawBehind {
                        drawRect(
                            brush = Brush.verticalGradient(
                                0f to Color(0xFFFFD998).copy(alpha = 0.06f),
                                0.32f to Color.Transparent,
                                1f to Color.Black.copy(alpha = 0.18f),
                            ),
                        )
                        val center = Offset(size.width * 0.5f, size.height * 0.45f)
                        val radius = size.maxDimension * 0.72f
                        drawCircle(
                            brush = Brush.radialGradient(
                                listOf(Color.Transparent, Color.Black.copy(alpha = 0.30f)),
                                center = center,
                                radius = radius,
                            ),
                            center = center,
                            radius = radius,
                        )
                    },
            )
        }
        content()
    }
}

/** Re-export of BoxScope so callers of [FantasyScreenBackground] don't need the foundation import. */
typealias BoxScope = androidx.compose.foundation.layout.BoxScope


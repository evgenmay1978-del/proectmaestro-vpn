package com.maestrovpn.tv.compose.fantasy

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
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
    Row(
        modifier = modifier
            .fillMaxWidth()
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .then(
                if (onClick != null) {
                    Modifier.clickable(interactionSource = interaction, indication = null, role = Role.Button) { onClick() }
                } else {
                    Modifier
                },
            )
            .fantasyFrame(R.drawable.frame_bar)
            .fantasyFocusFrame({ focusAlpha }, NeonGreen)
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Start,
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = iconTint, modifier = Modifier.size(24.dp))
            Spacer(Modifier.width(14.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(title, color = Color(0xFFF1EEE6), fontWeight = FontWeight.SemiBold, fontSize = 16.sp)
            if (!subtitle.isNullOrBlank()) {
                Text(subtitle, color = GoldMid.copy(alpha = 0.8f), fontSize = 12.5.sp)
            }
        }
        if (trailing != null) {
            Spacer(Modifier.width(10.dp))
            trailing()
        }
    }
}

/**
 * Dark-oak page background for the list/settings screens — a tiled carved-wood texture
 * (`oak_bg`, sliced from the эскиз) filling the whole screen. Content is drawn on top.
 */
@Composable
fun FantasyScreenBackground(
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    Box(modifier.fillMaxSize()) {
        androidx.compose.foundation.Image(
            painter = painterResource(R.drawable.oak_bg),
            contentDescription = null,
            modifier = Modifier.matchParentSize(),
            contentScale = ContentScale.Crop,
        )
        content()
    }
}

/** Re-export of BoxScope so callers of [FantasyScreenBackground] don't need the foundation import. */
typealias BoxScope = androidx.compose.foundation.layout.BoxScope

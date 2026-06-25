package com.maestrovpn.tv.compose.component

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.theme.GlassBottom
import com.maestrovpn.tv.compose.theme.GlassTop
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Shared "spider / green-glass" UI kit. Every interactive surface is a dark glass
 * plate with a thin NEON-GREEN border + a coloured focus glow (real colored shadow
 * on API 28+), matching the owner's reference (spiderinterfeis.png). SELECTION and
 * the primary CTA switch the accent to orange. All controls are plain Material 3 so
 * they're D-pad-focusable on a TV AND tappable on a phone.
 */
internal fun glassBrush() = Brush.verticalGradient(listOf(GlassTop, GlassBottom))

/**
 * Glass pill with a neon border + optional leading icon. Used for protocol chips,
 * secondary actions and contact links. [selected] flips the accent to orange.
 * [iconTint] overrides the icon colour (e.g. brand-coloured contact logos).
 */
@Composable
fun NeonChip(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    selected: Boolean = false,
    iconTint: Color? = null,
) {
    val shape = RoundedCornerShape(16.dp)
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val accent = if (selected) MaestroOrange else NeonGreen
    val scale by animateFloatAsState(
        if (pressed) 0.97f else if (focused) 1.06f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "chipScale",
    )
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 18.dp, vertical = 12.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .shadow(
                elevation = if (focused) 18.dp else 8.dp,
                shape = shape,
                clip = false,
                ambientColor = accent,
                spotColor = accent,
            )
            .background(glassBrush(), shape)
            .border(if (focused) 2.dp else 1.4.dp, accent.copy(alpha = if (focused) 1f else 0.75f), shape),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = iconTint ?: accent, modifier = Modifier.size(20.dp))
            Spacer(Modifier.width(10.dp))
        }
        Text(
            label,
            color = if (selected) MaestroOrange else Color.White,
            fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
        )
    }
}

/**
 * Glossy raised pill for the primary CTAs — "Купить подписку" (orange) and the phone
 * number (green). A 3-stop vertical gradient (light → accent → dark) reads as a domed
 * glossy button; the coloured shadow gives the neon glow.
 */
@Composable
fun GlossyButton(
    label: String,
    onClick: () -> Unit,
    accent: Color,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
) {
    val shape = RoundedCornerShape(18.dp)
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        if (pressed) 0.96f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "glossyScale",
    )
    val brush = Brush.verticalGradient(
        listOf(lerp(accent, Color.White, 0.42f), accent, lerp(accent, Color.Black, 0.34f)),
    )
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 24.dp, vertical = 14.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .shadow(
                elevation = if (focused) 22.dp else 12.dp,
                shape = shape,
                clip = false,
                ambientColor = accent,
                spotColor = accent,
            )
            .background(brush, shape)
            .border(if (focused) 2.dp else 0.dp, if (focused) Color.White else Color.Transparent, shape),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = Color.White, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(10.dp))
        }
        Text(label, fontWeight = FontWeight.Bold, fontSize = 17.sp)
    }
}

/** Embossed green badge (rounded square) holding an icon — the account-card adornments. */
@Composable
private fun GreenBadge(icon: ImageVector, modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(46.dp)
            .shadow(8.dp, RoundedCornerShape(13.dp), clip = false, ambientColor = NeonGreen, spotColor = NeonGreen)
            .clip(RoundedCornerShape(13.dp))
            .background(
                Brush.verticalGradient(
                    listOf(lerp(NeonGreen, Color.White, 0.30f), NeonGreen, lerp(NeonGreen, Color.Black, 0.32f)),
                ),
            ),
    ) {
        Icon(icon, contentDescription = null, tint = Color(0xFF06210F), modifier = Modifier.size(26.dp))
    }
}

/**
 * Account card — dark glass pill with a neon-green outline, a person badge on the
 * left, the login + days-left in the middle, and a calendar badge on the right.
 */
@Composable
fun NeonAccountCard(
    login: String?,
    daysText: String?,
    daysColor: Color,
    modifier: Modifier = Modifier,
    leadingIcon: ImageVector,
    trailingIcon: ImageVector,
) {
    val shape = RoundedCornerShape(18.dp)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        modifier = modifier
            .shadow(10.dp, shape, clip = false, ambientColor = NeonGreen, spotColor = NeonGreen)
            .background(glassBrush(), shape)
            .border(1.4.dp, NeonGreen.copy(alpha = 0.8f), shape)
            .padding(horizontal = 14.dp, vertical = 12.dp),
    ) {
        GreenBadge(leadingIcon)
        Column(modifier = Modifier.padding(horizontal = 2.dp)) {
            if (!login.isNullOrBlank()) {
                Text(
                    "Аккаунт: $login",
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                )
            }
            if (!daysText.isNullOrBlank()) {
                Spacer(Modifier.height(2.dp))
                Text(daysText, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Black, color = daysColor)
            }
        }
        GreenBadge(trailingIcon)
    }
}

/** Spaced uppercase section label ("ПРОТОКОЛ", "КОНТАКТЫ"). */
@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaestroSilver,
        letterSpacing = 2.sp,
        modifier = modifier,
    )
}

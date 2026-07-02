package com.maestrovpn.tv.compose.component

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.theme.ChromeDark
import com.maestrovpn.tv.compose.theme.ChromeHi
import com.maestrovpn.tv.compose.theme.ChromeLight
import com.maestrovpn.tv.compose.theme.ChromeLow
import com.maestrovpn.tv.compose.theme.ChromeMid
import com.maestrovpn.tv.compose.theme.ChromeOrangeHi
import com.maestrovpn.tv.compose.theme.ChromeOrangeLow
import com.maestrovpn.tv.compose.theme.GlassBottom
import com.maestrovpn.tv.compose.theme.GlassTop
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Shared "spider / chrome-glass" UI kit. Every interactive surface is a dark glass
 * plate FRAMED IN BRUSHED CHROME — the same polished-steel bezel as the spider-medallion
 * ring — so the whole app shares one metal language on every screen. Green is kept only
 * for icons / status; SELECTION + the primary CTA switch the accent (and the bezel) to
 * orange. A coloured glow appears on focus (TV D-pad) and on the selected chip. All
 * controls are plain Material 3 so they're D-pad-focusable on a TV AND tappable on a phone.
 */
internal fun glassBrush() = Brush.verticalGradient(listOf(GlassTop, GlassBottom))

/**
 * The brushed-chrome bezel brush (top-lit → steel → dark edge), identical to the
 * medallion ring. [selected] swaps it to a warm orange-lit steel for the active control.
 */
internal fun chromeBezelBrush(selected: Boolean = false): Brush =
    if (selected) {
        Brush.verticalGradient(listOf(ChromeOrangeHi, MaestroOrange, ChromeOrangeLow, ChromeDark))
    } else {
        Brush.verticalGradient(
            0f to ChromeHi, 0.12f to ChromeLight, 0.5f to ChromeMid, 0.82f to ChromeLow, 1f to ChromeDark,
        )
    }

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
    subtitle: String? = null,
    locked: Boolean = false,
) {
    val shape = RoundedCornerShape(16.dp)
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val accent = if (selected) MaestroOrange else NeonGreen
    // glow: neutral dark at rest (chrome reads clean), accent only on focus / selection
    val glow = if (selected) MaestroOrange else if (focused) NeonGreen else Color.Black
    val scale by animateFloatAsState(
        if (pressed) 0.97f else if (focused) 1.06f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "chipScale",
    )
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 10.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale; alpha = if (locked) 0.55f else 1f }
            .shadow(
                elevation = if (focused) 16.dp else if (selected) 12.dp else 6.dp,
                shape = shape,
                clip = false,
                ambientColor = glow,
                spotColor = glow,
            )
            .background(glassBrush(), shape)
            .border(if (focused) 2.dp else 1.5.dp, chromeBezelBrush(selected), shape),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = iconTint ?: accent, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(7.dp))
        }
        if (subtitle != null) {
            // two-line chip: protocol name + a small recommendation BADGE (unified style).
            // In the narrow 3-per-row grid long RU names (Hysteria2 / NaiveProxy / Vless+Reality)
            // must FIT, so the name wraps to a 2nd line instead of being ellipsised mid-word.
            Column(Modifier.weight(1f, fill = false)) {
                Text(
                    label,
                    color = if (selected) MaestroOrange else Color.White,
                    fontWeight = FontWeight.Bold,
                    fontSize = 12.sp,
                    lineHeight = 13.sp,
                    maxLines = 2,
                    softWrap = true,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    subtitle,
                    color = (if (selected) MaestroOrange else NeonGreen).copy(alpha = 0.85f),
                    fontWeight = FontWeight.Medium,
                    fontSize = 9.sp,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        } else {
            Text(
                label,
                color = if (selected) MaestroOrange else Color.White,
                fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
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
            .border(if (focused) 2.5.dp else 1.5.dp, chromeBezelBrush(), shape),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = Color.White, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(10.dp))
        }
        Text(label, fontWeight = FontWeight.Bold, fontSize = 17.sp)
    }
}

/**
 * Vertical chrome tile — a brushed-chrome framed glass plate with the icon stacked
 * ABOVE a short, centred label. Used for the secondary-action grid (icon-on-top tiles).
 */
@Composable
fun ChromeTile(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    iconTint: Color = NeonGreen,
) {
    val shape = RoundedCornerShape(14.dp)
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        if (pressed) 0.97f else if (focused) 1.05f else 1f,
        tween(140, easing = FastOutSlowInEasing), label = "tileScale",
    )
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 12.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .shadow(
                elevation = if (focused) 16.dp else 6.dp,
                shape = shape,
                clip = false,
                ambientColor = if (focused) NeonGreen else Color.Black,
                spotColor = if (focused) NeonGreen else Color.Black,
            )
            .background(glassBrush(), shape)
            .border(if (focused) 2.dp else 1.5.dp, chromeBezelBrush(), shape),
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Icon(icon, contentDescription = null, tint = iconTint, modifier = Modifier.size(24.dp))
            Spacer(Modifier.height(7.dp))
            Text(
                label,
                color = Color.White,
                fontWeight = FontWeight.Medium,
                fontSize = 13.sp,
                textAlign = TextAlign.Center,
                maxLines = 2,
            )
        }
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
            // glassmorphism inner highlight — a soft top sheen clipped to the rounded card, so
            // the plate reads as lit glass (blur is unavailable pre-31, this is the cheap look).
            .drawBehind {
                val cr = 18.dp.toPx()
                drawRoundRect(
                    brush = Brush.verticalGradient(
                        0f to Color.White.copy(alpha = 0.12f),
                        0.45f to Color.Transparent,
                    ),
                    cornerRadius = CornerRadius(cr, cr),
                )
            }
            .border(1.5.dp, chromeBezelBrush(), shape)
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

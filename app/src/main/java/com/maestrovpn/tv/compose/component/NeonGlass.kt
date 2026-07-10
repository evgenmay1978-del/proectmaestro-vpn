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
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.theme.ChromeDark
import com.maestrovpn.tv.compose.theme.ChromeHi
import com.maestrovpn.tv.compose.theme.ChromeLight
import com.maestrovpn.tv.compose.theme.ChromeLow
import com.maestrovpn.tv.compose.theme.ChromeMid
import com.maestrovpn.tv.compose.theme.ChromeOrangeHi
import com.maestrovpn.tv.compose.theme.ChromeOrangeLow
import com.maestrovpn.tv.compose.theme.GlassBottom
import com.maestrovpn.tv.compose.theme.GlassTop
import com.maestrovpn.tv.compose.theme.GoldDark
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.PlayfairFamily
// GoldHi used both for the gold bezel and the phone SectionLabel colour
import com.maestrovpn.tv.compose.theme.GoldLow
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.MaestroSilver
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.WoodTileBottom
import com.maestrovpn.tv.compose.theme.WoodTileTop

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

// ── Wood/gold variants (derived from the эскиз). С ТВ-редизайна 2026-07-09 wood=true на ВСЕХ
// поверхностях (телефон + ТВ, единый Dark-Fantasy стиль); glass/chrome остаются wood=false
// fallback-ом. ──

/** Dark carved-wood tile fill (lit top → shadowed bottom) — the plate face. */
internal fun woodBrush() = Brush.verticalGradient(listOf(WoodTileTop, WoodTileBottom))

/**
 * The gold-bezel brush (bright lit edge → gold body → dark edge), the phone counterpart of
 * [chromeBezelBrush]. [selected] swaps it to the SAME warm orange-lit steel as chrome, so the
 * SELECTED state semantics are identical to TV/glass.
 */
internal fun goldBezelBrush(selected: Boolean = false): Brush =
    if (selected) {
        Brush.verticalGradient(listOf(ChromeOrangeHi, MaestroOrange, ChromeOrangeLow, ChromeDark))
    } else {
        Brush.verticalGradient(listOf(GoldHi, GoldMid, GoldLow, GoldDark))
    }

/** Plate face brush: wood on phone, glass on TV. */
private fun plateBrush(wood: Boolean) = if (wood) woodBrush() else glassBrush()

/** Bezel brush: gold on phone, chrome on TV. */
private fun bezelBrush(wood: Boolean, selected: Boolean = false) =
    if (wood) goldBezelBrush(selected) else chromeBezelBrush(selected)

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
    wood: Boolean = false,
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
            .then(
                // PHONE (wood) → real sliced aged-bronze NinePatch from the эскиз; TV → glass/chrome (unchanged).
                if (wood) Modifier.fantasyFrame(R.drawable.frame_button, selected)
                else Modifier.background(plateBrush(false), shape)
                    .border(if (focused) 2.dp else 1.5.dp, bezelBrush(false, selected), shape),
            ),
    ) {
        // Icon LEFT + text LEFT-aligned across the full width (matches the эскиз — not centered).
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.Start,
        ) {
            if (icon != null) {
                Icon(icon, contentDescription = null, tint = iconTint ?: accent, modifier = Modifier.size(22.dp))
                Spacer(Modifier.width(10.dp))
            }
            if (subtitle != null) {
                // two-line chip: protocol name + a small recommendation BADGE, LEFT-aligned.
                // NO wrap/ellipsis — autoSize shrinks long RU names (Hysteria2 / NaiveProxy) so they
                // FIT one line in the narrow 3-per-row cell.
                Column(Modifier.weight(1f)) {
                    BasicText(
                        text = label,
                        style = TextStyle(
                            color = if (selected) MaestroOrange else Color.White,
                            fontWeight = FontWeight.Bold,
                        ),
                        maxLines = 1,
                        autoSize = TextAutoSize.StepBased(
                            minFontSize = 10.sp, maxFontSize = 18.sp, stepSize = 0.5.sp,
                        ),
                    )
                    BasicText(
                        text = subtitle,
                        style = TextStyle(
                            color = (if (selected) MaestroOrange else NeonGreen).copy(alpha = 0.85f),
                            fontWeight = FontWeight.Medium,
                        ),
                        maxLines = 1,
                        autoSize = TextAutoSize.StepBased(
                            minFontSize = 7.sp, maxFontSize = 11.sp, stepSize = 0.5.sp,
                        ),
                    )
                }
            } else {
                Text(
                    label,
                    color = if (selected) MaestroOrange else Color.White,
                    fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
            }
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
    wood: Boolean = false,
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
    // Эталон owner (телефон/дерево): текст и иконка CTA — кремово-золотые, не белые.
    val content = if (wood) Color(0xFFEFE0B0) else Color.White
    Button(
        onClick = onClick,
        shape = shape,
        interactionSource = interaction,
        contentPadding = PaddingValues(horizontal = 24.dp, vertical = 14.dp),
        elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = content),
        modifier = modifier
            .graphicsLayer { scaleX = scale; scaleY = scale }
            .shadow(
                elevation = if (focused) 22.dp else 12.dp,
                shape = shape,
                clip = false,
                ambientColor = accent,
                spotColor = accent,
            )
            .then(
                // PHONE (wood) → wide bronze plaque frame (accent lives in the glow + text, not a bright dome);
                // TV → the accent-domed gradient + chrome bezel (unchanged).
                if (wood) Modifier.fantasyFrame(R.drawable.frame_bar)
                else Modifier.background(brush, shape)
                    .border(if (focused) 2.5.dp else 1.5.dp, bezelBrush(false), shape),
            ),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, tint = content, modifier = Modifier.size(22.dp))
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
    wood: Boolean = false,
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
            .then(
                if (wood) Modifier.fantasyFrame(R.drawable.frame_button)
                else Modifier.background(plateBrush(false), shape)
                    .border(if (focused) 2.dp else 1.5.dp, bezelBrush(false), shape),
            ),
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            // Эталон owner (телефон/дерево): иконка плитки крупнее, подпись чуть больше.
            Icon(icon, contentDescription = null, tint = iconTint, modifier = Modifier.size(if (wood) 28.dp else 24.dp))
            Spacer(Modifier.height(7.dp))
            Text(
                label,
                color = Color.White,
                fontWeight = FontWeight.Medium,
                fontSize = if (wood) 14.sp else 13.sp,
                textAlign = TextAlign.Center,
                maxLines = 2,
            )
        }
    }
}

/** Embossed green badge (rounded square) holding an icon — the account-card adornments. */
@Composable
private fun GreenBadge(icon: ImageVector, modifier: Modifier = Modifier, big: Boolean = false) {
    // PHONE (эскиз) → larger, richer JADE-glass badge to match the mockup; TV → original bright chip.
    val sz = if (big) 48.dp else 46.dp      // эскиз-бейдж ≈47dp → почти как было; НЕ раздувать
    val rad = if (big) 14.dp else 13.dp
    // deeper emerald base on phone (was acid-bright NeonGreen) → reads as polished jade, not neon plastic.
    val base = if (big) Color(0xFF27A85E) else NeonGreen
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(sz)
            .shadow(if (big) 10.dp else 8.dp, RoundedCornerShape(rad), clip = false, ambientColor = NeonGreen, spotColor = NeonGreen)
            .clip(RoundedCornerShape(rad))
            .background(
                Brush.verticalGradient(
                    listOf(lerp(base, Color.White, 0.34f), base, lerp(base, Color.Black, 0.38f)),
                ),
            ),
    ) {
        Icon(icon, contentDescription = null, tint = Color(0xFF06210F), modifier = Modifier.size(if (big) 27.dp else 26.dp))
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
    wood: Boolean = false,
) {
    val shape = RoundedCornerShape(18.dp)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        modifier = modifier
            .shadow(10.dp, shape, clip = false, ambientColor = NeonGreen, spotColor = NeonGreen)
            .then(
                // PHONE (wood) → real sliced aged-bronze panel NinePatch; TV → glass plate + sheen + chrome bezel.
                if (wood) {
                    Modifier.fantasyFrame(R.drawable.frame_panel)
                } else {
                    Modifier
                        .background(plateBrush(false), shape)
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
                        .border(1.5.dp, bezelBrush(false), shape)
                }
            )
            .padding(horizontal = 14.dp, vertical = 12.dp),
    ) {
        GreenBadge(leadingIcon, big = wood)
        // login + days CENTERED between the two badges (weight fills the space; text centered) —
        // matches the эскиз (was left-aligned = «Безлимит не по центру»).
        Column(
            modifier = Modifier.weight(1f).padding(horizontal = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            if (!login.isNullOrBlank()) {
                Text(
                    "Аккаунт: $login",
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                    textAlign = TextAlign.Center,
                )
            }
            if (!daysText.isNullOrBlank()) {
                Spacer(Modifier.height(2.dp))
                Text(
                    daysText,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Black,
                    color = daysColor,
                    textAlign = TextAlign.Center,
                )
            }
        }
        GreenBadge(trailingIcon, big = wood)
    }
}

/** Spaced uppercase section label ("ПРОТОКОЛ", "КОНТАКТЫ"). With wood=true (phone + TV — единый
 *  Dark-Fantasy стиль) it reads GOLD to match the carved-wood frame; silver otherwise. */
@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier, wood: Boolean = false) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        fontFamily = PlayfairFamily,
        fontWeight = FontWeight.Bold,
        color = if (wood) GoldHi else MaestroSilver,
        letterSpacing = 3.sp,
        modifier = modifier,
    )
}

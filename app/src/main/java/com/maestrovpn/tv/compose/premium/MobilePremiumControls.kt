package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame

@Composable
fun MobilePremiumButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    leadingIcon: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = modifier
            .defaultMinSize(
                minWidth = PremiumTouchTarget,
                minHeight = PremiumTouchTarget,
            )
            .clickable(
                enabled = enabled,
                role = Role.Button,
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .fantasyFrame(R.drawable.frame_button)
            .alpha(if (enabled) 1f else 0.46f)
            // fantasyFrame does not apply the nine-patch's optical content inset.
            .padding(PremiumButtonContentPadding),
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (leadingIcon != null) {
            leadingIcon()
            Spacer(Modifier.width(10.dp))
        }
        Text(
            text = label,
            color = PremiumText,
            fontWeight = FontWeight.SemiBold,
            fontSize = 16.sp,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
fun MobilePremiumTextField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    focusRequester: FocusRequester? = null,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .defaultMinSize(minHeight = PremiumTouchTarget)
            .fantasyFrame(R.drawable.frame_bar)
            .alpha(if (enabled) 1f else 0.46f)
            // Keep text clear of the painted frame; the drawable padding is not consumed.
            .padding(PremiumTextFieldContentPadding),
        contentAlignment = Alignment.CenterStart,
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier
                .fillMaxWidth()
                .semantics { contentDescription = placeholder }
                .then(
                    if (focusRequester != null) {
                        Modifier.focusRequester(focusRequester)
                    } else {
                        Modifier
                    },
                ),
            enabled = enabled,
            singleLine = true,
            textStyle = TextStyle(
                color = PremiumText,
                fontSize = 16.sp,
                lineHeight = 22.sp,
            ),
            cursorBrush = SolidColor(PremiumGold),
            decorationBox = { innerTextField ->
                if (value.isEmpty()) {
                    Text(
                        text = placeholder,
                        color = PremiumTextMuted,
                        fontSize = 16.sp,
                    )
                }
                innerTextField()
            },
        )
    }
}

@Composable
fun MobilePremiumLoading(
    message: String,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = 32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(18.dp),
    ) {
        CircularProgressIndicator(color = PremiumEmerald)
        Text(
            text = message,
            color = PremiumText,
            fontSize = 16.sp,
        )
    }
}

@Composable
fun MobilePremiumError(
    message: String,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
    retryLabel: String = stringResource(R.string.menu_redo),
) {
    MobilePremiumPanel(modifier) {
        Column(
            modifier = Modifier.fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            Icon(
                imageVector = Icons.Default.ErrorOutline,
                contentDescription = null,
                tint = PremiumRuby,
                modifier = Modifier.size(52.dp),
            )
            Text(
                text = message,
                color = PremiumRuby,
                fontSize = 17.sp,
            )
            MobilePremiumButton(
                label = retryLabel,
                onClick = onRetry,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
fun MobilePremiumSegmented(
    options: List<String>,
    selectedIndex: Int,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .fantasyFrame(R.drawable.frame_bar)
            // A single frame groups the options; each option remains its own 48dp target.
            .padding(PremiumSegmentedContentPadding),
    ) {
        options.forEachIndexed { index, label ->
            val selected = index == selectedIndex
            Box(
                modifier = Modifier
                    .weight(1f)
                    .defaultMinSize(minHeight = PremiumTouchTarget)
                    .background(
                        color = if (selected) {
                            PremiumEmerald.copy(alpha = 0.20f)
                        } else {
                            PremiumWalnut.copy(alpha = 0.28f)
                        },
                        shape = RoundedCornerShape(12.dp),
                    )
                    .selectable(
                        selected = selected,
                        role = Role.RadioButton,
                        onClick = { onSelect(index) },
                    )
                    .padding(horizontal = 10.dp, vertical = 12.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = label,
                    color = if (selected) PremiumText else PremiumTextMuted,
                    fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
fun MobilePremiumAppRow(
    label: String,
    supportingText: String?,
    icon: ImageBitmap?,
    selected: Boolean,
    onSelectedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .defaultMinSize(minHeight = PremiumTouchTarget)
            .background(
                color = if (selected) {
                    PremiumEmerald.copy(alpha = 0.13f)
                } else {
                    PremiumLeather.copy(alpha = 0.72f)
                },
                shape = RoundedCornerShape(PremiumPanelCorner),
            )
            .toggleable(
                value = selected,
                role = Role.Checkbox,
                onValueChange = onSelectedChange,
            )
            .padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Image(
                bitmap = icon,
                contentDescription = null,
                modifier = Modifier.size(40.dp),
            )
            Spacer(Modifier.width(12.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(
                text = label,
                color = PremiumText,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (!supportingText.isNullOrBlank()) {
                Text(
                    text = supportingText,
                    color = PremiumTextMuted,
                    fontSize = 12.sp,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.width(12.dp))
        Icon(
            imageVector = if (selected) Icons.Default.CheckCircle else Icons.Default.RadioButtonUnchecked,
            contentDescription = null,
            tint = if (selected) PremiumEmerald else PremiumGoldMuted,
            modifier = Modifier
                .size(28.dp)
                .clearAndSetSemantics {},
        )
    }
}

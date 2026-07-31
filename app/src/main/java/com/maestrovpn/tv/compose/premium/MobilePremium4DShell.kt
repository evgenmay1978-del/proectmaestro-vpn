package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.rememberIsTv
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import kotlin.math.roundToInt

/** Lightweight phone-only shell. It deliberately owns neither the home atlas nor hero art. */
@Composable
fun MobilePremium4DShell(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    backContentDescription: String = stringResource(R.string.content_description_back),
    content: @Composable ColumnScope.() -> Unit,
) {
    if (!mobilePremiumShellEnabled(rememberIsTv())) {
        Column(modifier = modifier.fillMaxSize(), content = content)
        return
    }

    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val layoutMode = mobilePremiumLayoutMode(
            widthDp = maxWidth.value.roundToInt(),
            heightDp = maxHeight.value.roundToInt(),
        )
        val horizontalPadding = mobilePremiumHorizontalPadding(layoutMode).dp
        val maximumContentWidth = mobilePremiumMaximumContentWidth(layoutMode).dp

        MobilePremium4DBackground(Modifier.fillMaxSize())

        Column(
            modifier = Modifier
                .fillMaxSize()
                .testTag("premium-mobile-shell")
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .imePadding(),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            MobilePremiumTopBar(
                title = title,
                onBack = onBack,
                backContentDescription = backContentDescription,
                compact = layoutMode == MobilePremiumLayoutMode.Compact,
                modifier = Modifier
                    .widthIn(max = maximumContentWidth)
                    .fillMaxWidth()
                    .padding(horizontal = horizontalPadding),
            )
            Column(
                modifier = Modifier
                    .widthIn(max = maximumContentWidth)
                    .fillMaxWidth()
                    .weight(1f)
                    .padding(horizontal = horizontalPadding),
                content = content,
            )
        }
    }
}

@Composable
fun MobilePremiumTopBar(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    backContentDescription: String = stringResource(R.string.content_description_back),
    compact: Boolean = false,
) {
    val titleSize = if (compact) 25.sp else 30.sp
    Row(
        modifier = modifier
            .defaultMinSize(minHeight = if (compact) 56.dp else 64.dp)
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(
            onClick = onBack,
            modifier = Modifier.defaultMinSize(
                minWidth = PremiumTouchTarget,
                minHeight = PremiumTouchTarget,
            ),
        ) {
            Icon(
                imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                contentDescription = backContentDescription,
                tint = PremiumGold,
            )
        }
        Text(
            text = title,
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 12.dp),
            color = PremiumGold,
            fontFamily = PlayfairFamily,
            fontWeight = FontWeight.SemiBold,
            fontSize = titleSize,
            lineHeight = (titleSize.value + 5).sp,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
fun MobilePremiumDialogSurface(
    title: String,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .widthIn(max = PremiumDialogMaximumWidth)
            .testTag("premium-dialog-surface")
            .fantasyFrame(R.drawable.frame_panel)
            .padding(PremiumDialogContentPadding),
    ) {
        Text(
            text = title,
            color = PremiumGold,
            fontFamily = PlayfairFamily,
            fontWeight = FontWeight.SemiBold,
            fontSize = 24.sp,
            lineHeight = 29.sp,
        )
        Spacer(Modifier.height(16.dp))
        Column(
            modifier = Modifier.fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            content = content,
        )
    }
}

@Composable
fun MobilePremiumSheetSurface(
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .testTag("premium-sheet-surface")
            .fantasyFrame(R.drawable.frame_panel)
            .padding(PremiumSheetContentPadding),
        content = content,
    )
}

@Composable
private fun MobilePremium4DBackground(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .testTag("premium-mobile-shell-background")
            .background(PremiumWalnut)
            .drawBehind {
                drawRect(
                    brush = Brush.verticalGradient(
                        colors = listOf(PremiumLeather, PremiumWalnut, PremiumShellShadow),
                    ),
                )
                val lightX = size.width * 0.5f
                drawCircle(
                    brush = Brush.radialGradient(
                        colors = listOf(
                            PremiumGold.copy(alpha = 0.10f),
                            Color.Transparent,
                        ),
                        center = androidx.compose.ui.geometry.Offset(lightX, size.height * 0.18f),
                        radius = size.maxDimension * 0.64f,
                    ),
                    center = androidx.compose.ui.geometry.Offset(lightX, size.height * 0.18f),
                    radius = size.maxDimension * 0.64f,
                )
            },
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(4.dp)
                .fantasyFrame(R.drawable.frame_panel)
                .alpha(PremiumShellFrameAlpha),
        )
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(Color.Black.copy(alpha = PremiumShellReadabilityScrimAlpha)),
        )
    }
}

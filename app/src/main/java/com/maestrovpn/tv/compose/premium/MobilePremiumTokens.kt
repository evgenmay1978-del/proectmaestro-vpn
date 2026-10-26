package com.maestrovpn.tv.compose.premium

import androidx.compose.ui.graphics.Color
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.ui.unit.dp

internal val PremiumWalnut = Color(0xFF120D09)
internal val PremiumLeather = Color(0xFF1D1510)
internal val PremiumGold = Color(0xFFE0BD70)
internal val PremiumGoldMuted = Color(0xFFA8874A)
internal val PremiumEmerald = Color(0xFF29C965)
// Осветлён с 0xFFC9564D: на тёмном дереве прежний оттенок давал контраст 3,6–4,5 при норме 4,5.
internal val PremiumRuby = Color(0xFFE0705F)
internal val PremiumText = Color(0xFFF1E8D0)
internal val PremiumTextMuted = Color(0xFFBDB5A7)
internal val PremiumTouchTarget = 48.dp
// Вкладки телефона считают отступ как 8,5% ширины (24…42dp) — на SM-S948B это 35dp. Экраны настроек
// должны попадать в ту же рамку, иначе плашки шире и наезжают на резную лозу.
internal val PremiumPhoneContentInset = 35.dp
internal val PremiumPanelCorner = 18.dp

// Nine-patch artwork is visual-only; callers use these explicit content insets.
internal val PremiumPanelContentPadding = PaddingValues(horizontal = 22.dp, vertical = 20.dp)
internal val PremiumButtonContentPadding = PaddingValues(horizontal = 22.dp, vertical = 13.dp)
internal val PremiumTextFieldContentPadding = PaddingValues(horizontal = 22.dp, vertical = 14.dp)
internal val PremiumSegmentedContentPadding = PaddingValues(7.dp)
internal val PremiumDialogContentPadding = PaddingValues(horizontal = 24.dp, vertical = 22.dp)
internal val PremiumSheetContentPadding = PaddingValues(horizontal = 20.dp, vertical = 18.dp)
internal val PremiumDialogMaximumWidth = 560.dp
internal val PremiumShellShadow = Color(0xFF090604)
internal const val PremiumShellFrameAlpha = 0.78f
internal const val PremiumShellReadabilityScrimAlpha = 0.18f

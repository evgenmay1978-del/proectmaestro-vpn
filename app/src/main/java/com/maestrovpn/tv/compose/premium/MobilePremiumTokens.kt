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
// Утверждённый тёмный мобильный дизайн (23.11.2026, прототип
// «MaestroVPN Mobile — моё видение»): чистый тёмный экран без деревянной рамки.
// Значения — точные из прототипа; старые Premium*-токены остаются для глубоких
// экранов настроек, которые редизайн не трогает.
internal val DarkBg = Color(0xFF0D0B08)
internal val DarkPanel = Color(0xFF17130E)
internal val DarkPanel2 = Color(0xFF1F1A12)
internal val DarkLine = Color(0x2EDBA84A) // rgba(219,168,74,.18)
internal val DarkLineSoft = Color(0x1ADBA84A) // rgba(219,168,74,.10)
internal val DarkText = Color(0xFFF7F0DD)
internal val DarkMuted = Color(0xFFAB9F86)
internal val DarkFaint = Color(0xFF7D7260)
internal val DarkGoldLight = Color(0xFFF6DA8A)
internal val DarkGold = Color(0xFFD9A441)
internal val DarkGoldDark = Color(0xFF8A5F14)
internal val DarkGreen = Color(0xFF2FE0A4)
internal val DarkAmber = Color(0xFFF0B64C)
internal val DarkRed = Color(0xFFF2707A)
internal val DarkPanelCorner = 16.dp
internal val DarkCardCorner = 20.dp
internal val DarkNavCorner = 28.dp
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

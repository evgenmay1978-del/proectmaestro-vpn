package com.maestrovpn.tv.compose.theme

import androidx.compose.ui.graphics.Color

// MaestroVPN brand palette — matches the app icon (orange fox on a silver shield,
// black background): vivid orange accent, metallic silver, near-black surfaces.
val MaestroOrange = Color(0xFFF0792A)
val MaestroOrangeDark = Color(0xFFC25A14)
val MaestroOrangeLight = Color(0xFFFF9D52)
val MaestroSilver = Color(0xFFB8B8C0)
// "Spider" reference (spiderinterfeis.png): deep near-black like rgb(6,9,14).
val MaestroBg = Color(0xFF06090E)
val MaestroSurface = Color(0xFF0E1217)
val MaestroSurfaceHi = Color(0xFF181D24)

// "Spider" theme accents — NEON GREEN is the primary glow/border/icon colour
// (status, account days, protocol/action icons, phone). Orange (MaestroOrange) is
// kept for SELECTION + the primary CTA (buy) + the active-protocol line.
val NeonGreen = Color(0xFF46E05A)
val NeonGreenBright = Color(0xFF6BF06B)
val NeonGreenDeep = Color(0xFF123A22)
// Dark translucent "glass" plate (top sheen → darker bottom) for chips/cards.
val GlassTop = Color(0xFF1B232B)
val GlassBottom = Color(0xFF090D12)

// Brushed-CHROME bezel — the SAME polished-steel as the spider-medallion ring.
// Every control is framed in this so the whole UI shares one metal language:
// bright lit top edge → cool steel → mid grey body → dark lower → near-black edge.
val ChromeHi = Color(0xFFF2F4F7)
val ChromeLight = Color(0xFFC7CCD2)
val ChromeMid = Color(0xFF9AA0A6)
val ChromeLow = Color(0xFF3A3D42)
val ChromeDark = Color(0xFF15171A)
// Warm "lit chrome" stops for a SELECTED control's bezel (orange-tinted steel).
val ChromeOrangeHi = Color(0xFFF6C79A)
val ChromeOrangeLow = Color(0xFF7A2F0C)

// ── PHONE-ONLY wood/gold palette (derived from the эскиз `home_eskiz.png`). Used ONLY on
// the phone (!isTv) so the live tiles/cards match the ornate carved-wood + gold-bezel frame
// of the эскиз. TV keeps the chrome/glass language untouched. ──
val WoodTileTop = Color(0xFF2A1D10)     // dark wood tile — lit top
val WoodTileBottom = Color(0xFF120B05)  // dark wood tile — shadowed bottom
val GoldHi = Color(0xFFE8C877)          // gold bezel — bright lit edge
val GoldMid = Color(0xFFC9A24A)         // gold bezel — body
val GoldLow = Color(0xFF8A6D2F)         // gold bezel — lower
val GoldDark = Color(0xFF3A2C14)        // gold bezel — dark edge

// kept for any code still referencing the old names (now mapped to the brand)
val SingBoxPrimary = MaestroOrange
val SingBoxPrimaryDark = MaestroOrangeDark
val SingBoxPrimaryLight = MaestroOrangeLight

// Service status colors
val ServiceRunning = Color(0xFF4CAF50)
val ServiceStopped = Color(0xFF9E9E9E)
val ServiceError = Color(0xFFF44336)

// Log colors
val LogRed = Color(0xFFFF2158)
val LogGreen = Color(0xFF2ECC71)
val LogYellow = Color(0xFFE5E500)
val LogBlue = Color(0xFF3498DB)
val LogPurple = Color(0xFFE500E5)
val LogRedLight = Color(0xFFE91E63)
val LogBlueLight = Color(0xFF00A6B2)
val LogWhite = Color(0xFFECECEC)

// Material You seed color
val SeedColor = Color(0xFFD81B60)

// Additional semantic colors
val SuccessGreen = Color(0xFF4CAF50)
val WarningOrange = Color(0xFFFF9800)
val ErrorRed = Color(0xFFF44336)
val InfoBlue = Color(0xFF2196F3)

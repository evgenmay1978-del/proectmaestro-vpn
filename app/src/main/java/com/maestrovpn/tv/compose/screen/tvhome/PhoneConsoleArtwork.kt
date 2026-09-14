package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.premium.ApprovedMobileBrand
import kotlin.math.roundToInt

// Phone-only materials. Shared premium/TV tokens and artwork remain untouched.
internal val ConsoleWalnut = Color(0xFF17120E)
internal val ConsoleGold = Color(0xFFC79A4B)
internal val ConsoleGoldLight = Color(0xFFE8C677)
internal val ConsoleText = Color(0xFFF3E7CB)
internal val ConsoleTextMuted = Color(0xFFB79D75)
internal val ConsoleEmerald = Color(0xFF20C878)
internal val ConsoleCoral = Color(0xFFD46A61)
internal val ConsoleShape = RoundedCornerShape(8.dp)

/** One inset profile, without a separate wood bitmap or a second cast shadow. */
internal fun Modifier.phoneConsolePanel(selected: Boolean = false): Modifier =
    clip(ConsoleShape).drawWithCache {
        val inset = 1.dp.toPx()
        val radius = CornerRadius(8.dp.toPx())
        val metal = Brush.linearGradient(
            listOf(ConsoleGoldLight.copy(alpha = .8f), ConsoleGold, Color(0xFF705127)),
            Offset.Zero, Offset(size.width, size.height),
        )
        val surface = if (selected) Brush.linearGradient(
            listOf(Color(0xEB174431), Color(0xEF092A1C)), Offset.Zero, Offset(size.width, size.height),
        ) else Brush.linearGradient(
            listOf(Color(0x64231B12), Color(0xB3100C08)), Offset.Zero, Offset(size.width, size.height),
        )
        onDrawBehind {
            drawRoundRect(surface, cornerRadius = radius)
            drawRoundRect(metal, topLeft = Offset(inset, inset),
                size = Size(size.width - inset * 2, size.height - inset * 2),
                cornerRadius = radius, style = Stroke(1.dp.toPx()))
            // Fine inner lip, lit from the upper left; all panels share the same metal profile.
            drawRoundRect(ConsoleGold.copy(alpha = .18f), topLeft = Offset(inset * 3, inset * 3),
                size = Size((size.width - inset * 6).coerceAtLeast(0f), (size.height - inset * 6).coerceAtLeast(0f)),
                cornerRadius = CornerRadius(6.dp.toPx()), style = Stroke(.5.dp.toPx()))
        }
    }

@Composable
private fun PhoneConsoleBackground(modifier: Modifier) {
    val atlas = ImageBitmap.imageResource(R.drawable.mobile_approved_atlas)
    Box(modifier.background(ConsoleWalnut)) {
        Image(painterResource(R.drawable.phone_home_wood), null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        Canvas(Modifier.fillMaxSize()) {
            // Retain the current console's side carvings; they meet the content at one common inset.
            val rail = 24.dp.toPx().roundToInt()
            drawImage(atlas, IntOffset(0, 0), IntSize(72, 1628),
                IntOffset.Zero, IntSize(rail, size.height.roundToInt()))
            drawImage(atlas, IntOffset(780, 0), IntSize(72, 1628),
                IntOffset(size.width.roundToInt() - rail, 0), IntSize(rail, size.height.roundToInt()))
        }
    }
}

/** Insets are consumed once. Navigation stays outside the scrollable, naturally sized content. */
@Composable
internal fun PhoneConsoleLayout(
    activeTab: String,
    onHome: () -> Unit,
    onServers: () -> Unit,
    onAccount: () -> Unit,
    onSettings: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.(Dp) -> Unit,
) {
    Box(modifier.fillMaxSize()) {
        PhoneConsoleBackground(Modifier.fillMaxSize())
        BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding()) {
            val contentWidth = (maxWidth - 48.dp).coerceAtLeast(0.dp)
            val heroWidth = minOf(contentWidth, (maxHeight - contentWidth / 5f - 520.dp).coerceIn(208.dp, 320.dp))
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                Column(
                    Modifier.weight(1f).fillMaxWidth().padding(horizontal = 24.dp)
                        .testTag("phone-console-scroll").verticalScroll(rememberScrollState())
                        .padding(top = 8.dp, bottom = 16.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    // Keep the approved wordmark's aspect ratio and original carved lettering.
                    ApprovedMobileBrand(Modifier.fillMaxWidth().aspectRatio(724f / 145f))
                    content(heroWidth)
                }
                PhoneBottomNavigation(activeTab, onHome, onServers, onAccount, onSettings,
                    Modifier.fillMaxWidth().padding(horizontal = 24.dp).padding(bottom = 8.dp))
            }
        }
    }
}

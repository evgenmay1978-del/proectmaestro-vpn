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
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.imageResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.premium.ApprovedMobileBrand
import com.maestrovpn.tv.compose.premium.approvedMobilePanel
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
internal fun Modifier.phoneConsolePanel(selected: Boolean = false, navigation: Boolean = false): Modifier =
    approvedMobilePanel(selected = selected, navigation = navigation)

@Composable
private fun PhoneConsoleBackground(modifier: Modifier) {
    Box(modifier.background(ConsoleWalnut)) {
        Image(painterResource(R.drawable.phone_home_wood), null, Modifier.fillMaxSize(), contentScale = ContentScale.FillBounds)
        Image(painterResource(R.drawable.phone_home_frame), null, Modifier.fillMaxSize(), contentScale = ContentScale.FillBounds)
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
            // The reference medallion nearly fills the inner console width; keeping it at
            // 299.dp made the phone candidate read as the older compact screen.
            val heroWidth = minOf(contentWidth, (maxHeight - contentWidth / 5f - 470.dp).coerceIn(240.dp, 380.dp))
            val view = LocalView.current
            androidx.compose.runtime.SideEffect {
                // Let the approved wood continue under Android's bars without drawing fake
                // status/navigation indicators in the app itself. TV never composes this layout.
                (view.context as? android.app.Activity)?.window?.apply {
                    statusBarColor = ConsoleWalnut.toArgb()
                    navigationBarColor = ConsoleWalnut.toArgb()
                    if (android.os.Build.VERSION.SDK_INT >= 29) {
                        isStatusBarContrastEnforced = false
                        isNavigationBarContrastEnforced = false
                    }
                }
            }
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                Column(
                    Modifier.weight(1f).fillMaxWidth().padding(horizontal = 24.dp)
                        .testTag("phone-console-scroll").verticalScroll(rememberScrollState())
                        .padding(top = 8.dp, bottom = 16.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(5.dp),
                ) {
                    // Keep the approved wordmark's aspect ratio and original carved lettering.
                    ApprovedMobileBrand(Modifier.fillMaxWidth().aspectRatio(724f / 120f))
                    content(heroWidth)
                }
                PhoneBottomNavigation(activeTab, onHome, onServers, onAccount, onSettings,
                    Modifier.fillMaxWidth().padding(horizontal = 24.dp).padding(bottom = 8.dp))
            }
        }
    }
}

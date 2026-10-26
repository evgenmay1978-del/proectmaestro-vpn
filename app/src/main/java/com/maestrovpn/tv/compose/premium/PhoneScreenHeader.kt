package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/**
 * One phone header for every screen: a carved panel with the gold back arrow and a live title.
 *
 * Looks exactly like [MobilePremiumTopBar] from the approved phone shell, but deliberately does
 * NOT carry a compaction flag or a fixed height: the title may wrap to two lines and the panel
 * must grow with it. The TV branch of a screen keeps its own Material3 TopAppBar.
 */
@Composable
fun PhoneScreenHeader(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    backContentDescription: String? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .approvedMobilePanel()
            .defaultMinSize(minHeight = 52.dp)
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
                .padding(horizontal = 4.dp),
            // Кремовый, как PhonePageTitle на вкладках: заголовок экрана одного цвета везде.
            color = PremiumText,
            fontFamily = FontFamily.SansSerif,
            fontWeight = FontWeight.SemiBold,
            fontSize = 20.sp,
            lineHeight = 25.sp,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        actions()
    }
}

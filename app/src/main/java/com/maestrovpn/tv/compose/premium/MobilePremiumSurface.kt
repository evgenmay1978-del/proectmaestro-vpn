package com.maestrovpn.tv.compose.premium

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.platform.testTag
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame

@Composable
fun MobilePremiumScreen(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    backContentDescription: String = stringResource(R.string.content_description_back),
    content: @Composable ColumnScope.() -> Unit,
) {
    Box(modifier = modifier.fillMaxSize()) {
        MobilePremium4DShell(
            title = title,
            onBack = onBack,
            modifier = Modifier
                .fillMaxSize()
                .testTag("premium-mobile-screen"),
            backContentDescription = backContentDescription,
            content = content,
        )
    }
}

@Composable
fun MobilePremiumPanel(
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .semantics(mergeDescendants = false) {}
            .fantasyFrame(R.drawable.frame_panel)
            // fantasyFrame draws the nine-patch but does not consume its content padding.
            .padding(PremiumPanelContentPadding),
        content = content,
    )
}

package com.maestrovpn.tv.compose.fantasy

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen

/**
 * Dark-Fantasy text input — replaces the raw Material3 [androidx.compose.material3.OutlinedTextField]
 * on phone. A carved dark-wood panel with an aged-bronze frame (sliced from the эскиз); the cursor
 * is gold, the placeholder dim bronze, and a soft EMERALD halo blooms around the frame while focused.
 *
 * @param focusRequester attached to the INNER field (so a screen can auto-focus it for the D-pad).
 */
@Composable
fun FantasyTextField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    placeholder: String? = null,
    enabled: Boolean = true,
    singleLine: Boolean = true,
    focusRequester: FocusRequester? = null,
    keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    visualTransformation: VisualTransformation = VisualTransformation.None,
) {
    var focused by remember { mutableStateOf(false) }
    Box(
        modifier
            .then(
                if (focused) {
                    Modifier.shadow(
                        elevation = 12.dp,
                        shape = androidx.compose.foundation.shape.RoundedCornerShape(18.dp),
                        clip = false,
                        ambientColor = NeonGreen,
                        spotColor = NeonGreen,
                    )
                } else {
                    Modifier
                },
            )
            .fantasyFrame(R.drawable.frame_bar)
            .heightIn(min = 60.dp)
            .padding(horizontal = 24.dp, vertical = 16.dp),
        contentAlignment = Alignment.CenterStart,
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            singleLine = singleLine,
            keyboardOptions = keyboardOptions,
            visualTransformation = visualTransformation,
            textStyle = TextStyle(color = Color.White, fontSize = 18.sp, fontWeight = FontWeight.Medium),
            cursorBrush = SolidColor(GoldHi),
            modifier = Modifier
                // Fill the decorated frame: the tap/focus target is the BasicTextField's own
                // layout — wrapped to the text width, ~80% of the visible field ignored taps.
                .fillMaxWidth()
                .then(if (focusRequester != null) Modifier.focusRequester(focusRequester) else Modifier)
                .onFocusChanged { focused = it.isFocused },
            decorationBox = { inner ->
                if (value.isEmpty() && !placeholder.isNullOrBlank()) {
                    androidx.compose.material3.Text(
                        placeholder,
                        color = GoldMid.copy(alpha = 0.6f),
                        fontSize = 18.sp,
                    )
                }
                inner()
            },
        )
    }
}

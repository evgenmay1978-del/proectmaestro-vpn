package com.maestrovpn.tv.compose.premium

import androidx.compose.animation.core.animateDpAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ChevronRight
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.compose.theme.PlayfairFamily

/**
 * Общие компоненты утверждённого тёмного мобильного дизайна (23.11.2026, прототип
 * «MaestroVPN Mobile — моё видение»). Все размеры/цвета/тексты — точные из прототипа.
 * Шрифты прототипа (Manrope/Unbounded/Cinzel) в приложении отсутствуют — используется
 * системный sans (заголовки — semibold) и Playfair для буквы аватара; это единственное
 * сознательное отступление по шрифтам.
 */

/** Золотой градиент кнопок/активных элементов прототипа (сверху вниз). */
internal val DarkGoldGradient: Brush
    get() = Brush.verticalGradient(listOf(DarkGoldLight, DarkGold))

/** Плоская тёмная карточка прототипа (радиус 20dp). */
fun Modifier.darkCard(selected: Boolean = false): Modifier =
    this
        .background(if (selected) DarkPanel2 else DarkPanel, RoundedCornerShape(DarkCardCorner))
        .border(
            1.dp,
            if (selected) Color(0x8CD9A441) else DarkLineSoft, // rgba(217,164,65,.55) у выбранной
            RoundedCornerShape(DarkCardCorner),
        )

/** Заголовок экрана прототипа (21sp, semibold). */
@Composable
internal fun DarkScreenTitle(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        color = DarkText,
        fontSize = 21.sp,
        lineHeight = 26.sp,
        fontWeight = FontWeight.SemiBold,
        modifier = modifier.padding(top = 8.dp),
    )
}

/** Подзаголовок экрана прототипа (13.5sp, muted). */
@Composable
internal fun DarkScreenSubtitle(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        color = DarkMuted,
        fontSize = 13.5.sp,
        lineHeight = 19.sp,
        modifier = modifier.padding(top = 5.dp),
    )
}

/** Заголовок группы прототипа (11.5sp, uppercase, faint). */
@Composable
internal fun DarkGroupLabel(text: String, modifier: Modifier = Modifier) {
    Text(
        text.uppercase(),
        color = DarkFaint,
        fontSize = 11.5.sp,
        fontWeight = FontWeight.Bold,
        letterSpacing = 0.09.em,
        modifier = modifier.padding(start = 2.dp, top = 20.dp, bottom = 9.dp),
    )
}

/** Кнопка прототипа: primary — золотой градиент, secondary — тёмная с золотой обводкой. */
@Composable
internal fun DarkButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    primary: Boolean = false,
    icon: ImageVector? = null,
) {
    val shape = RoundedCornerShape(DarkPanelCorner)
    Row(
        modifier
            .heightIn(min = 48.dp)
            .then(
                if (primary) {
                    Modifier
                        .shadow(8.dp, shape, ambientColor = Color(0x80D9A441), spotColor = Color(0x80D9A441))
                        .background(DarkGoldGradient, shape)
                } else {
                    Modifier
                        .background(DarkPanel2, shape)
                        .border(1.dp, DarkLine, shape)
                },
            )
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Center,
    ) {
        if (icon != null) {
            Icon(icon, null, tint = if (primary) Color(0xFF2C1E04) else DarkGoldLight, modifier = Modifier.size(19.dp))
            Spacer(Modifier.width(7.dp))
        }
        Text(
            label,
            color = if (primary) Color(0xFF2C1E04) else DarkGoldLight,
            fontSize = 14.5.sp,
            fontWeight = FontWeight.Bold,
            textAlign = TextAlign.Center,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Опасная кнопка прототипа (красная обводка/текст). */
@Composable
internal fun DarkDangerButton(label: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(DarkPanelCorner)
    Box(
        modifier
            .fillMaxWidth()
            .heightIn(min = 50.dp)
            .background(Color(0x12F2707A), shape) // rgba(242,112,122,.07)
            .border(1.dp, Color(0x59F2707A), shape) // rgba(242,112,122,.35)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 11.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, color = DarkRed, fontSize = 15.sp, fontWeight = FontWeight.Bold)
    }
}

/** Переключатель прототипа (50×30dp). */
@Composable
internal fun DarkSwitch(checked: Boolean, onCheckedChange: (Boolean) -> Unit, modifier: Modifier = Modifier) {
    val knobOffset by animateDpAsState(if (checked) 23.dp else 3.dp, label = "dark-switch")
    Box(
        modifier
            .size(50.dp, 30.dp)
            .clip(CircleShape)
            .then(
                if (checked) Modifier.background(DarkGoldGradient)
                else Modifier.background(Color(0xFF38312A)),
            )
            .clickable(role = Role.Switch, onClick = { onCheckedChange(!checked) }),
    ) {
        Box(
            Modifier
                .offset(x = knobOffset, y = 3.dp)
                .size(24.dp)
                .clip(CircleShape)
                .background(if (checked) Color(0xFFFFF7E0) else Color(0xFFCFC6B2)),
        )
    }
}

/** Строка версии прототипа (12.5sp, faint, по центру). */
@Composable
internal fun DarkVersionLine(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        color = DarkFaint,
        fontSize = 12.5.sp,
        fontWeight = FontWeight.SemiBold,
        textAlign = TextAlign.Center,
        modifier = modifier.fillMaxWidth().padding(top = 16.dp),
    )
}

/** Ячейка статистики прототипа (значение + подпись). */
@Composable
internal fun DarkStatCell(value: String, label: String, modifier: Modifier = Modifier) {
    Column(
        modifier
            .background(DarkPanel, RoundedCornerShape(14.dp))
            .border(1.dp, DarkLineSoft, RoundedCornerShape(14.dp))
            .padding(vertical = 9.dp, horizontal = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(value, color = DarkText, fontSize = 15.5.sp, fontWeight = FontWeight.Bold, maxLines = 1)
        Text(
            label.uppercase(),
            color = DarkFaint,
            fontSize = 10.5.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = 0.06.em,
            maxLines = 1,
        )
    }
}

/** Плитка-значок 34dp в строках меню прототипа. */
@Composable
internal fun DarkIconTile(icon: ImageVector, modifier: Modifier = Modifier) {
    Box(
        modifier
            .size(34.dp)
            .background(Color(0x1CD9A441), RoundedCornerShape(10.dp)), // rgba(217,164,65,.11)
        contentAlignment = Alignment.Center,
    ) {
        Icon(icon, null, tint = DarkGoldLight, modifier = Modifier.size(18.dp))
    }
}

/** Строка меню прототипа (заголовок + подзаголовок + значение/переключатель/шеврон). */
@Composable
internal fun DarkMenuRow(
    title: String,
    sub: String? = null,
    icon: ImageVector,
    value: String? = null,
    checked: Boolean? = null,
    onCheckedChange: ((Boolean) -> Unit)? = null,
    onClick: (() -> Unit)? = null,
    showChevron: Boolean = false,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier
            .fillMaxWidth()
            .heightIn(min = 54.dp)
            .then(if (onClick != null) Modifier.clickable(role = Role.Button, onClick = onClick) else Modifier)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        DarkIconTile(icon)
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(title, color = DarkText, fontSize = 15.sp, fontWeight = FontWeight.Bold, maxLines = 1)
            if (sub != null) {
                Text(sub, color = DarkMuted, fontSize = 12.5.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        if (value != null) {
            Text(value, color = DarkMuted, fontSize = 13.5.sp, fontWeight = FontWeight.Bold)
        }
        if (checked != null) {
            Spacer(Modifier.width(8.dp))
            DarkSwitch(checked, onCheckedChange ?: {})
        }
        if (showChevron) {
            Spacer(Modifier.width(8.dp))
            Icon(Icons.Default.ChevronRight, null, tint = DarkFaint, modifier = Modifier.size(18.dp))
        }
    }
}

/** Список меню прототипа (одна карточка со строками и разделителями). */
@Composable
internal fun DarkMenuList(
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(modifier.darkCard()) {
        content()
    }
}

/** Разделитель строк меню (1dp, line-soft). */
@Composable
internal fun DarkMenuDivider() {
    Spacer(Modifier.fillMaxWidth().height(1.dp).background(DarkLineSoft))
}

/** Маленький бейдж прототипа (золотой или зелёный). */
@Composable
internal fun DarkBadge(text: String, green: Boolean = false, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(999.dp)
    Box(
        modifier
            .background(
                if (green) Color(0x242FE0A4) else Color(0x21D9A441), // green-dim / gold .13
                shape,
            )
            .border(
                1.dp,
                if (green) Color(0x402FE0A4) else DarkLineSoft,
                shape,
            )
            .padding(horizontal = 9.dp, vertical = 4.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text,
            color = if (green) DarkGreen else DarkGoldLight,
            fontSize = 11.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = 0.04.em,
            maxLines = 1,
        )
    }
}

/** Аватар с первой буквой логина (золотой градиент, буква Playfair — ближайший
 *  к Cinzel из прототипа шрифт в приложении). */
@Composable
internal fun DarkAvatar(letter: String, modifier: Modifier = Modifier) {
    Box(
        modifier
            .size(42.dp)
            .clip(CircleShape)
            .background(
                Brush.radialGradient(
                    0f to Color(0xFFFFE9A8),
                    0.45f to Color(0xFFEEC25E),
                    1f to Color(0xFFB9822C),
                ),
            )
            .border(1.dp, Color(0x805A3C08), CircleShape), // rgba(90,60,8,.5)
        contentAlignment = Alignment.Center,
    ) {
        Text(
            letter.uppercase().take(1),
            color = Color(0xFF43300A),
            fontSize = 19.sp,
            fontWeight = FontWeight.Bold,
            fontFamily = PlayfairFamily,
        )
    }
}

/** Сегментированный переключатель прототипа («Обычный VPN» / «CDN»). */
@Composable
internal fun DarkSegmented(
    options: List<String>,
    selectedIndex: Int,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier
            .fillMaxWidth()
            .background(DarkPanel, CircleShape)
            .border(1.dp, DarkLineSoft, CircleShape)
            .padding(4.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        options.forEachIndexed { index, label ->
            val selected = index == selectedIndex
            Box(
                Modifier
                    .weight(1f)
                    .heightIn(min = 42.dp)
                    .then(
                        if (selected) Modifier
                            .shadow(4.dp, CircleShape, ambientColor = Color(0x59D9A441), spotColor = Color(0x59D9A441))
                            .background(DarkGoldGradient, CircleShape)
                        else Modifier.background(Color.Transparent, CircleShape),
                    )
                    .clickable(role = Role.Button, onClick = { onSelect(index) })
                    .padding(horizontal = 8.dp, vertical = 10.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    label,
                    color = if (selected) Color(0xFF2C1E04) else DarkMuted,
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** Двухстрочный чип главной (подпись + значение; опциональный золотой суффикс
 *  значения в той же строке, как «ГБ» в «115,35 ГБ»). */
@Composable
internal fun RowScope.DarkChip(
    label: String,
    value: String,
    goldSuffix: String? = null,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier
            .weight(1f)
            .background(DarkPanel, RoundedCornerShape(DarkPanelCorner))
            .border(1.dp, DarkLineSoft, RoundedCornerShape(DarkPanelCorner))
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 13.dp, vertical = 11.dp),
    ) {
        Text(
            label.uppercase(),
            color = DarkFaint,
            fontSize = 11.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = 0.08.em,
            maxLines = 1,
        )
        Text(
            text = androidx.compose.ui.text.AnnotatedString.Builder().apply {
                val main = if (goldSuffix != null && value.endsWith(goldSuffix)) {
                    value.removeSuffix(goldSuffix)
                } else {
                    value
                }
                append(main)
                if (goldSuffix != null && value.endsWith(goldSuffix)) {
                    withStyle(
                        androidx.compose.ui.text.SpanStyle(color = DarkGoldLight, fontSize = 15.sp),
                    ) { append(goldSuffix) }
                }
            }.toAnnotatedString(),
            color = if (goldSuffix == null) DarkGoldLight else DarkText,
            fontSize = 16.5.sp,
            fontWeight = FontWeight.Bold,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Поле ввода в тёмном стиле (для ключа НейроПротокола). */
@Composable
internal fun DarkTextField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    modifier: Modifier = Modifier,
    singleLine: Boolean = true,
) {
    androidx.compose.foundation.text.BasicTextField(
        value = value,
        onValueChange = onValueChange,
        singleLine = singleLine,
        textStyle = androidx.compose.ui.text.TextStyle(
            color = DarkText,
            fontSize = 14.sp,
        ),
        modifier = modifier
            .fillMaxWidth()
            .background(DarkPanel, RoundedCornerShape(DarkPanelCorner))
            .border(1.dp, DarkLineSoft, RoundedCornerShape(DarkPanelCorner))
            .padding(horizontal = 16.dp, vertical = 13.dp),
        decorationBox = { inner ->
            if (value.isEmpty()) {
                Text(placeholder, color = DarkFaint, fontSize = 14.sp)
            }
            inner()
        },
    )
}

package com.maestrovpn.tv.compose.screen.tvhome

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.absoluteOffset
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.Chat
import androidx.compose.material.icons.filled.CloudDownload
import androidx.compose.material.icons.filled.Forum
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.ShoppingCart
import androidx.compose.material.icons.filled.Smartphone
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.premium.MobilePremiumButton
import com.maestrovpn.tv.compose.premium.PremiumEmerald
import com.maestrovpn.tv.compose.premium.PremiumGold
import com.maestrovpn.tv.compose.premium.PremiumGoldMuted
import com.maestrovpn.tv.compose.premium.PremiumText
import com.maestrovpn.tv.compose.premium.PremiumTextMuted
import com.maestrovpn.tv.compose.premium.PremiumTouchTarget
import com.maestrovpn.tv.compose.theme.MaestroOrange
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.utils.ConnectivityCheck
import com.maestrovpn.tv.vendor.Vendor
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlin.math.abs
import kotlin.math.max

/**
 * Phone-only control deck — единственный владелец всего, что ниже 4D-героя.
 *
 * Заменяет `PhoneRevolverMenu` целиком. Раскладка не выдумана: каждая секция стоит ровно в
 * тех границах, что измерены по выбранному владельцем эталону
 * `design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg` и зафиксированы в
 * чистой [phoneHomeReferenceLayout] с JVM-тестом.
 *
 * ⛔ ЛОВУШКИ, из-за которых предыдущий APK был отвергнут (не возвращать):
 *  1. Старый барабан лежал полноэкранным слоем ПОВЕРХ новой 4D-сцены, а его градиентная
 *     маска (walnut alpha 0.88 сверху / leather 0.92 снизу) гасила арт — получалась
 *     «чёрная плита». Здесь нет ни одной полноэкранной подложки и ни одной маски: дека
 *     начинается на `layout.deckTop`, выше живут только герой и глаз, и они ловят касания.
 *  2. `graphicsLayer { rotationX }` + snap-fling (цилиндр) ломали попадание пальцем и
 *     TalkBack. Дуга протоколов здесь — статический вертикальный сдвиг плитки: без
 *     вращения, без привязки к скроллу, без снэпа и хаптик-тиков.
 *  3. Видимый орнамент бывает ниже 48 dp (телефонная пилюля эталона — 38 dp). Размер
 *     ОРНАМЕНТА берётся из эталона, а область нажатия расширяется до
 *     [PhoneHomeReferenceLayout.minimumInteractiveHeight] вокруг того же центра.
 */
@Composable
internal fun PhoneHomeControlDeck(
    statusText: String,
    connected: Boolean,
    connecting: Boolean,
    activeProtocol: String?,
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    protocols: List<String>,
    selected: String?,
    hasSubProfile: Boolean,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit,
    onBuy: () -> Unit,
    onEnterCode: () -> Unit,
    onSplitTunnel: () -> Unit,
    onShareIos: () -> Unit,
    onScanQr: () -> Unit,
    onEnterTrial: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    val open: (String) -> Unit = remember(context) {
        { url ->
            runCatching {
                context.startActivity(
                    Intent(Intent.ACTION_VIEW, Uri.parse(url))
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            }
        }
    }
    val onUpdate: () -> Unit = {
        (context as? Activity)?.let { activity ->
            scope.launch(Dispatchers.IO) {
                runCatching { Vendor.checkUpdate(activity, true) }
            }
        }
    }
    val onCheckConnection: () -> Unit = {
        scope.launch(Dispatchers.IO) {
            val ok = ConnectivityCheck.isOnline()
            withContext(Dispatchers.Main) {
                Toast.makeText(
                    context,
                    if (ok) "Соединение работает" else "Нет соединения",
                    Toast.LENGTH_SHORT,
                ).show()
            }
        }
    }

    BoxWithConstraints(modifier = modifier.testTag("home-control-deck")) {
        val layout = phoneHomeReferenceLayout(maxWidth.value, maxHeight.value)
        val deckTop = layout.deckTop
        val deckWidth = maxWidth.value
        val arcProtocols = remember(protocols) { orderedHomeProtocols(protocols) }

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(top = deckTop.dp)
                .verticalScroll(rememberScrollState()),
        ) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(layout.primaryDeckContentHeight.dp),
            ) {
                // ⛔ ЛОВУШКА: сюда напрашивался готовый `PhoneStatusRow`, но у него своя
                // вертикальная раскладка (padding 6 dp + spacer 8 dp), и строка протокола
                // уезжала на 398–418 dp — прямо под телефонную пилюлю (её цель нажатия
                // начинается на 402 dp). На эталоне статус занимает 363–386, протокол
                // 386–406, телефон 407: обе строки стоят по своим измеренным границам.
                // ⛔ Красная точка над словом «ПОДКЛЮЧЕНИЕ» — это ярлык, который врёт:
                // тот же класс дефекта, что «Загрузка…» над установкой обновления на ТВ.
                // Промежуточное состояние получает свой цвет, а не цвет отказа.
                val statusColor = when {
                    connected -> NeonGreen
                    connecting -> MaestroOrange
                    else -> StatusRed
                }
                DeckSection(layout.status.acrossViewport(deckWidth), deckTop) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.testTag("premium-status"),
                    ) {
                        Box(
                            Modifier
                                .size(11.dp)
                                .clip(CircleShape)
                                .background(statusColor),
                        )
                        Spacer(Modifier.width(9.dp))
                        Text(
                            text = statusText.uppercase(),
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.Bold,
                            color = statusColor,
                        )
                    }
                }

                val protocolLine = homeActiveProtocolLine(connected, activeProtocol, selected)
                if (protocolLine != null) {
                    DeckSection(layout.activeProtocol.acrossViewport(deckWidth), deckTop) {
                        Text(
                            text = protocolLine,
                            style = MaterialTheme.typography.titleSmall,
                            fontWeight = FontWeight.Bold,
                            color = MaestroOrange,
                            textAlign = TextAlign.Center,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier
                                .fillMaxWidth()
                                .testTag("home-active-protocol"),
                        )
                    }
                }

                DeckSection(layout.phone, deckTop, layout.minimumInteractiveHeight) {
                    MobilePremiumButton(
                        label = SUPPORT_PHONE_LABEL,
                        onClick = { open("tel:$SUPPORT_PHONE_URI") },
                        modifier = Modifier
                            .fillMaxWidth()
                            .testTag("home-action-phone"),
                        leadingIcon = { Icon(Icons.Filled.Call, null, tint = PremiumEmerald) },
                    )
                }

                DeckSection(layout.supportNote, deckTop) {
                    Text(
                        text = SUPPORT_NOTE,
                        style = MaterialTheme.typography.bodyMedium,
                        color = PremiumTextMuted,
                        textAlign = TextAlign.Center,
                        modifier = Modifier
                            .fillMaxWidth()
                            .testTag("home-support-note"),
                    )
                }

                DeckSection(layout.contacts, deckTop) {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(CONTACT_GAP.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.fillMaxSize(),
                    ) {
                        HomeTile(
                            label = "Telegram",
                            icon = Icons.Filled.Send,
                            onClick = { open("https://t.me/wapmixx") },
                            modifier = Modifier
                                .weight(1f)
                                .fillMaxSize()
                                .testTag("home-contact-telegram"),
                        )
                        HomeTile(
                            label = "МАКС",
                            icon = Icons.Filled.Forum,
                            onClick = { open("https://max.ru/") },
                            modifier = Modifier
                                .weight(1f)
                                .fillMaxSize()
                                .testTag("home-contact-max"),
                        )
                        HomeTile(
                            label = "WhatsApp",
                            icon = Icons.Filled.Chat,
                            onClick = { open("https://wa.me/79778116564") },
                            modifier = Modifier
                                .weight(1f)
                                .fillMaxSize()
                                .testTag("home-contact-whatsapp"),
                        )
                    }
                }

                ProtocolArc(
                    bounds = layout.protocolArc,
                    deckTop = deckTop,
                    protocols = arcProtocols,
                    selected = selected,
                    hasOlcrtcCreds = hasOlcrtcCreds,
                    olcrtcProvider = olcrtcProvider,
                    onSelectProtocol = onSelectProtocol,
                    onSelectOlcrtc = onSelectOlcrtc,
                )

                DeckSection(layout.buy, deckTop, layout.minimumInteractiveHeight) {
                    MobilePremiumButton(
                        label = "Купить подписку",
                        onClick = onBuy,
                        modifier = Modifier
                            .fillMaxWidth()
                            .testTag("home-action-buy"),
                        leadingIcon = { Icon(Icons.Filled.ShoppingCart, null, tint = PremiumGold) },
                    )
                }

                BottomConsole(
                    bounds = layout.bottomConsole,
                    deckTop = deckTop,
                    onEnterCode = onEnterCode,
                    onCheckConnection = onCheckConnection,
                    onShareIos = onShareIos,
                )
            }

            // Ниже первого экрана — то, чего нет в эталоне, но что обязано остаться
            // достижимым: иначе `trial`, `scanqr` и `split` выпадут из шести реальных
            // экранов приложения. Порядок первых 844 dp это не сдвигает.
            SecondaryDeck(
                accountLogin = accountLogin,
                daysLeft = daysLeft,
                accountExpires = accountExpires,
                hasSubProfile = hasSubProfile,
                onEnterTrial = onEnterTrial,
                onScanQr = onScanQr,
                onSplitTunnel = onSplitTunnel,
                onUpdate = onUpdate,
            )
        }
    }
}

/** Ставит секцию в измеренные границы эталона, но не ниже [minInteractive] по высоте. */
@Composable
private fun DeckSection(
    bounds: PhoneHomeReferenceBounds,
    deckTop: Float,
    minInteractive: Float = 0f,
    content: @Composable BoxScope.() -> Unit,
) {
    val height = max(bounds.bottom - bounds.top, minInteractive)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .absoluteOffset(
                x = bounds.left.dp,
                y = (bounds.centerY - height / 2f - deckTop).dp,
            )
            .size(width = bounds.right - bounds.left, height = height),
        content = content,
    )
}

private fun Modifier.size(width: Float, height: Float): Modifier = size(width.dp, height.dp)

/**
 * Тот же вертикальный диапазон, но на всю ширину вьюпорта. Прямоугольники статуса и
 * активного протокола в эталоне описывают ЭКСТЕНТ ТЕКСТА (137 и 140 dp), а не колонку:
 * если положить текст ровно в них, «Отключён: NaiveProxy • авто» перенесётся.
 */
private fun PhoneHomeReferenceBounds.acrossViewport(width: Float): PhoneHomeReferenceBounds =
    copy(left = 0f, right = width)

/**
 * Строка активного протокола под статусом — тот же текст, что показывал старый
 * `PhoneStatusRow`: протокол виден в ОБОИХ состояниях, а «• авто» дописывается, когда
 * выбран `auto`, но фактически поднят другой протокол.
 */
internal fun homeActiveProtocolLine(
    connected: Boolean,
    activeProtocol: String?,
    selected: String?,
): String? {
    val main = if (!activeProtocol.isNullOrBlank()) activeProtocol else selected
    if (main.isNullOrBlank()) return null
    val prefix = if (connected) "Подключён" else "Отключён"
    val viaAuto = selected == "auto" && main != "auto"
    return if (viaAuto) "$prefix: ${protocolLabel(main)}  •  авто" else "$prefix: ${protocolLabel(main)}"
}

/**
 * Шесть протоколов по дуге. Дуга — СТАТИЧЕСКИЙ вертикальный сдвиг сектора по параболе от
 * центра ряда: крайние опускаются на [ARC_DROP], средние почти не двигаются. Это не старый
 * цилиндр: ни `rotationX`, ни привязки к скроллу, ни снэпа — палец и TalkBack получают
 * обычный ряд радиокнопок.
 */
@Composable
private fun ProtocolArc(
    bounds: PhoneHomeReferenceBounds,
    deckTop: Float,
    protocols: List<String>,
    selected: String?,
    hasOlcrtcCreds: Boolean,
    olcrtcProvider: String?,
    onSelectProtocol: (String) -> Unit,
    onSelectOlcrtc: () -> Unit,
) {
    if (protocols.isEmpty()) return
    val width = bounds.right - bounds.left
    val scale = width / REFERENCE_WIDTH
    // Ячейки резного веера НЕ равномерны и НЕ повторяют силуэт дуги: сам силуэт провисает
    // к краям на 39.8 dp, а верх САМИХ ячеек — только на ~12 (замерено по home_arc_c.png).
    // Ставить сектор по силуэту нельзя: подпись уедет с резьбы на бортик.
    val cells = arcSectorCells(protocols.size)

    protocols.forEachIndexed { index, protocol ->
        val cell = cells.getOrNull(index) ?: return@forEachIndexed
        val locked = protocol == "olcrtc" && !hasOlcrtcCreds
        val isSelected = protocol == selected && !locked
        val provider = when {
            protocol != "olcrtc" -> null
            olcrtcProvider == "wbstream" -> "через WB"
            else -> "через Яндекс"
        }
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .absoluteOffset(
                    x = (bounds.left + (cell.centerDp - cell.widthDp / 2f) * scale).dp,
                    y = (cell.topDp * scale - deckTop).dp,
                )
                .size(width = cell.widthDp * scale, height = cell.heightDp * scale),
        ) {
            ProtocolSector(
                label = homeProtocolSectorLabel(protocol),
                icon = if (locked) Icons.Filled.Lock else protocolIcon(protocol),
                selected = isSelected,
                locked = locked,
                description = buildList {
                    add(protocolLabel(protocol))
                    add(protocolBadge(protocol))
                    provider?.let { add(it) }
                    if (locked) add("по запросу")
                }.joinToString(", "),
                onClick = { if (locked) onSelectOlcrtc() else onSelectProtocol(protocol) },
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("home-protocol-$protocol"),
            )
        }
    }
}

/** Одна ячейка резного веера: полностью измеренный интерьер, а не номинал из спеки. */
internal data class ArcSectorCell(
    val centerDp: Float,
    val widthDp: Float,
    val topDp: Float,
    val heightDp: Float,
)

/**
 * Центры семи ячеек заданы владельцем 2026-08-01: 39, 91, 143, 195, 247, 299, 351 dp — шаг 52,
 * симметрично относительно 195. Провис верха ячейки к краям — парабола с коэффициентом,
 * снятым с самого арта (`k=0.00048`, край d=156 → 11.7 dp).
 *
 * Если бэкенд вернул меньше семи протоколов, занимаются ЦЕНТРАЛЬНЫЕ ячейки, а крайние остаются
 * пустой резьбой: сдвигать ряд к левому краю нельзя — веер симметричен, и дыра сбоку читается
 * как брак сборки.
 */
internal fun arcSectorCells(count: Int): List<ArcSectorCell> {
    if (count <= 0) return emptyList()
    val all = ARC_SECTOR_CENTERS.indices.map { i ->
        ArcSectorCell(ARC_SECTOR_CENTERS[i], ARC_CELL_WIDTHS[i], ARC_CELL_TOPS[i], ARC_CELL_HEIGHTS[i])
    }
    if (count >= all.size) return all
    val first = (all.size - count) / 2
    return all.subList(first, first + count)
}

@Composable
private fun ProtocolSector(
    label: String,
    icon: ImageVector,
    selected: Boolean,
    locked: Boolean,
    description: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = modifier
            .defaultMinSize(minWidth = PremiumTouchTarget, minHeight = PremiumTouchTarget)
            // ⛔ Здесь стояла `fantasyFrame(frame_button)` — гладкий бронзовый кант ПОВЕРХ
            // резной ячейки веера. Это второй хозяин одной области: два канта друг на друге.
            // Рамку теперь рисует арт (`home_arc_*` в атласе), сектор даёт только подпись,
            // иконку, отметку выбора и зону нажатия.
            .alpha(if (locked) 0.72f else 1f)
            .selectable(
                selected = selected,
                role = Role.RadioButton,
                onClick = onClick,
            )
            .semantics { contentDescription = description }
            .padding(horizontal = 3.dp, vertical = 6.dp),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (selected) PremiumEmerald else PremiumGold,
            modifier = Modifier.size(20.dp),
        )
        Spacer(Modifier.height(4.dp))
        // ⛔ ЛОВУШКА (унаследована от старой плитки): «HYSTERIA2» и «NAIVEPROXY» шире
        // сектора, а дефолт Compose при maxLines=1 — TextOverflow.Clip, то есть обрез
        // посреди глифа даже без «…». autoSize сжимает кегль, а не режет слово.
        BasicText(
            text = label,
            style = TextStyle(
                color = if (selected) PremiumText else PremiumGoldMuted,
                fontWeight = FontWeight.Bold,
                textAlign = TextAlign.Center,
            ),
            maxLines = 2,
            autoSize = TextAutoSize.StepBased(
                minFontSize = 7.sp,
                maxFontSize = 11.sp,
                stepSize = 0.5.sp,
            ),
        )
        // Выбранный протокол помечается тонкой изумрудной чертой, а НЕ сплошной заливкой
        // карточки: заливка перекрывала резьбу и читалась как чужой слой поверх арта.
        Spacer(Modifier.height(3.dp))
        Box(
            modifier = Modifier
                .width(if (selected) SELECTION_BAR_WIDTH.dp else 0.dp)
                .height(2.dp)
                .clip(CircleShape)
                .background(PremiumEmerald),
        )
    }
}

/** Нижняя консоль эталона: «Ввести логин» — круглый «Тест сети» — «Подключить телефон». */
@Composable
private fun BottomConsole(
    bounds: PhoneHomeReferenceBounds,
    deckTop: Float,
    onEnterCode: () -> Unit,
    onCheckConnection: () -> Unit,
    onShareIos: () -> Unit,
) {
    val scale = (bounds.right - bounds.left) / CONSOLE_REFERENCE_WIDTH

    @Composable
    fun zone(left: Float, top: Float, right: Float, bottom: Float, content: @Composable () -> Unit) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .absoluteOffset(x = (left * scale).dp, y = (top * scale - deckTop).dp)
                .size(width = (right - left) * scale, height = (bottom - top) * scale),
        ) { content() }
    }

    zone(CONSOLE_LEFT[0], CONSOLE_LEFT[1], CONSOLE_LEFT[2], CONSOLE_LEFT[3]) {
        HomeTile(
            label = "Ввести логин",
            icon = Icons.Filled.Person,
            onClick = onEnterCode,
            framed = false,
            modifier = Modifier.fillMaxSize().testTag("home-action-login"),
        )
    }
    zone(CONSOLE_DIAL[0], CONSOLE_DIAL[1], CONSOLE_DIAL[2], CONSOLE_DIAL[3]) {
        HomeTile(
            label = "Тест сети",
            icon = Icons.Filled.Settings,
            onClick = onCheckConnection,
            framed = false,
            modifier = Modifier.fillMaxSize().testTag("home-action-network-test"),
        )
    }
    zone(CONSOLE_RIGHT[0], CONSOLE_RIGHT[1], CONSOLE_RIGHT[2], CONSOLE_RIGHT[3]) {
        HomeTile(
            label = "Подключить телефон",
            icon = Icons.Filled.Smartphone,
            onClick = onShareIos,
            framed = false,
            modifier = Modifier.fillMaxSize().testTag("home-action-share"),
        )
    }
}

/** Прямоугольная резная плитка: иконка сверху, подпись снизу. */
@Composable
private fun HomeTile(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    framed: Boolean = true,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = modifier
            .defaultMinSize(minWidth = PremiumTouchTarget, minHeight = PremiumTouchTarget)
            // ⛔ `framed = false` для зон нижней консоли: там резьбу даёт слой `console` атласа,
            // и своя рамка легла бы вторым кантом поверх первого. Ряд контактов рамку пока
            // сохраняет — своего арта под ним ещё нет.
            .then(if (framed) Modifier.fantasyFrame(R.drawable.frame_button) else Modifier)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 6.dp, vertical = 8.dp),
    ) {
        Icon(icon, contentDescription = null, tint = PremiumGold, modifier = Modifier.size(22.dp))
        Spacer(Modifier.height(6.dp))
        BasicText(
            text = label,
            style = TextStyle(
                color = PremiumText,
                fontWeight = FontWeight.Medium,
                textAlign = TextAlign.Center,
            ),
            maxLines = 2,
            autoSize = TextAutoSize.StepBased(
                minFontSize = 8.sp,
                maxFontSize = 12.sp,
                stepSize = 0.5.sp,
            ),
        )
    }
}

/** Всё, что не попало в первый экран эталона, но обязано остаться достижимым. */
@Composable
private fun SecondaryDeck(
    accountLogin: String?,
    daysLeft: Int?,
    accountExpires: String?,
    hasSubProfile: Boolean,
    onEnterTrial: () -> Unit,
    onScanQr: () -> Unit,
    onSplitTunnel: () -> Unit,
    onUpdate: () -> Unit,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(10.dp),
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 18.dp, top = 14.dp, end = 18.dp, bottom = 28.dp)
            .testTag("home-secondary-deck"),
    ) {
        if (!accountLogin.isNullOrBlank() || daysLeft != null) {
            AccountCard(
                login = accountLogin,
                daysLeft = daysLeft,
                expires = accountExpires,
                wood = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .widthIn(max = 460.dp)
                    .testTag("premium-account"),
            )
        }
        if (!hasSubProfile) {
            MobilePremiumButton(
                label = "Попробовать 2 дня бесплатно",
                onClick = onEnterTrial,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("home-action-trial"),
                leadingIcon = { Icon(Icons.Filled.Bolt, null, tint = PremiumEmerald) },
            )
        }
        MobilePremiumButton(
            label = "Сканировать QR",
            onClick = onScanQr,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("home-action-scan-qr"),
            leadingIcon = { Icon(Icons.Filled.QrCode2, null, tint = PremiumGold) },
        )
        MobilePremiumButton(
            label = "Приложения через VPN",
            onClick = onSplitTunnel,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("home-action-split"),
            leadingIcon = { Icon(Icons.Filled.Public, null, tint = PremiumGold) },
        )
        MobilePremiumButton(
            label = "Обновить приложение",
            onClick = onUpdate,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("home-action-update"),
            leadingIcon = { Icon(Icons.Filled.CloudDownload, null, tint = PremiumGold) },
        )
    }
}

/**
 * Порядок секторов дуги задан эталоном: Авто, VLESS, Hysteria2, AnyTLS, NaiveProxy, WEBRTC.
 * Между NaiveProxy и WEBRTC стоит WDTT (`vk-turn`): его нет на эталоне, но на телефоне это
 * рабочий протокол — прячет его только ТВ (`TvEskizHome.kt` filterNot), и терять его из
 * меню нельзя. Реальные теги приходят с бэкенда, поэтому неизвестные добавляются в конец, а
 * `olcrtc` присутствует всегда и последним — он показывается и без выданных кредов (замок +
 * запрос владельцу), ровно как в старом меню.
 */
internal fun orderedHomeProtocols(protocols: List<String>): List<String> {
    val known = HOME_PROTOCOL_ORDER.filter { it in protocols }
    val extra = protocols.filter { it !in HOME_PROTOCOL_ORDER }
    return (known + extra + "olcrtc").distinct()
}

/**
 * Подпись сектора. Отличается от [protocolLabel] только для `olcrtc`: на эталоне владелец
 * подписал его `WEBRTC`. Это ТОЛЬКО подпись — рантайм-тег, колбэк и семантика замка
 * остаются прежними.
 */
internal fun homeProtocolLabel(tag: String): String = when (tag) {
    "olcrtc" -> "WEBRTC"
    else -> protocolLabel(tag).uppercase()
}

/**
 * То же имя, но с ЯВНЫМ переносом там, где слово не влезает в сектор.
 *
 * ⛔ ЛОВУШКА: на эталоне секторов шесть и подписи выходят за плитку прямо на резьбу —
 * это запечённый арт, кодом так нельзя. С седьмым сектором (WDTT) на 390 dp плитка
 * получается 48 dp, и «NAIVEPROXY» даже на минимальных 7 sp не влезает: Compose ломает
 * длинное слово по символам и даёт «NAIVEPROX / Y». Перенос задан руками по слогу, а не
 * отдан авто-переносу.
 */
internal fun homeProtocolSectorLabel(tag: String): String = when (tag) {
    "naive" -> "NAIVE\nPROXY"
    else -> homeProtocolLabel(tag)
}

private val HOME_PROTOCOL_ORDER =
    listOf("auto", "vless", "hysteria2", "anytls", "naive", "vk-turn", "olcrtc")
private const val SUPPORT_PHONE_LABEL = "8 977 811-65-64"
private const val SUPPORT_PHONE_URI = "+79778116564"
private const val SUPPORT_NOTE = "Если я не ответил на звонок — напишите в любом из мессенджеров."
/** 1:1 к прежней строке статуса: ОТКЛЮЧЕНО = красная точка и красный текст. */
private val StatusRed = Color(0xFFFF4040)
private const val CONTACT_GAP = 10f
private const val REFERENCE_WIDTH = 390f
/** Центры ячеек резного веера, dp при ширине 390 (решение владельца 2026-08-01). */
private val ARC_SECTOR_CENTERS = listOf(37.8f, 88.3f, 141.7f, 195.6f, 248.2f, 301.4f, 352.4f)
/**
 * Интерьеры семи ячеек резного веера, dp при ширине 390 — ЗАМЕРЕНО по `home_arc_c.png` от 01.08
 * (alpha>200, luma<210), а не взято из спеки и не посчитано формулой.
 *
 * ⛔ ЛОВУШКА, из-за которой подписи «вылезали на дугу»: ячейки НЕ одинаковые и НЕ равны номиналу.
 * Спека обещала шаг 52 dp, и я ставил коробку 52 dp — а настоящие интерьеры от 40.1 до 47.3 dp,
 * то есть коробка была шире ячейки на 5…12 dp, и «HYSTERIA2», «NAIVEPROXY» и «АВТО» с иконкой
 * заезжали на резной разделитель. Крайние ячейки уже средних на 7 dp, центральная ниже соседних
 * на 9 dp (над ней замок), высота гуляет 67…84 dp.
 *
 * Центры при этом спеке соответствуют: расхождение не больше 2 dp.
 */
private val ARC_CELL_WIDTHS = listOf(40.1f, 47.0f, 45.7f, 47.3f, 45.7f, 46.4f, 40.7f)
private val ARC_CELL_TOPS = listOf(619.7f, 603.6f, 593.7f, 602.0f, 593.1f, 602.9f, 619.4f)
private val ARC_CELL_HEIGHTS = listOf(67.2f, 77.2f, 83.1f, 73.6f, 83.7f, 77.7f, 67.4f)
private const val SELECTION_BAR_WIDTH = 22f
/**
 * Три зоны действий нижней консоли, dp при ширине 390 — ЗАМЕРЕНО по `home_console_c.png` от 01.08
 * (тёмные интерьеры внутри резьбы). Круглая центральная зона выше боковых, как на эталоне.
 * Все три крупнее 48 dp: 107×60, 68×73, 107×60.
 */
private val CONSOLE_LEFT = listOf(32f, 757.4f, 139f, 817.4f)
private val CONSOLE_DIAL = listOf(160f, 749.7f, 228f, 823f)
private val CONSOLE_RIGHT = listOf(250f, 757.6f, 357f, 817.4f)
private const val CONSOLE_REFERENCE_WIDTH = 390f

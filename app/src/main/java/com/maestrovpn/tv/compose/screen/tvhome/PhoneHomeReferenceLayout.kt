package com.maestrovpn.tv.compose.screen.tvhome

internal data class PhoneHomeReferenceBounds(
    val left: Float,
    val top: Float,
    val right: Float,
    val bottom: Float,
) {
    val centerY: Float
        get() = (top + bottom) / 2f
}

internal data class PhoneHomeReferenceLayout(
    val heroScale: Float,
    val heroTranslationY: Float,
    val deckTop: Float,
    val primaryDeckBottom: Float,
    val primaryDeckContentHeight: Float,
    val minimumInteractiveHeight: Float,
    val requiresScroll: Boolean,
    val title: PhoneHomeReferenceBounds,
    val medallion: PhoneHomeReferenceBounds,
    val status: PhoneHomeReferenceBounds,
    val activeProtocol: PhoneHomeReferenceBounds,
    val phone: PhoneHomeReferenceBounds,
    val contacts: PhoneHomeReferenceBounds,
    val protocolArc: PhoneHomeReferenceBounds,
    val buy: PhoneHomeReferenceBounds,
    val bottomConsole: PhoneHomeReferenceBounds,
)

internal fun phoneHomeReferenceLayout(width: Float, height: Float): PhoneHomeReferenceLayout {
    require(width > 0f && height > 0f) { "Phone Home viewport dimensions must be positive" }

    val scale = width / ReferenceWidth
    fun bounds(left: Float, top: Float, right: Float, bottom: Float) = PhoneHomeReferenceBounds(
        left = left * scale,
        top = top * scale,
        right = right * scale,
        bottom = bottom * scale,
    )

    val bottomConsole = bounds(8f, 760f, 382f, 864f)
    val deckTop = DeckTop * scale
    val primaryDeckBottom = bottomConsole.bottom
    val primaryDeckContentHeight = primaryDeckBottom - deckTop
    return PhoneHomeReferenceLayout(
        heroScale = 1f,
        heroTranslationY = HeroTranslationY * scale,
        deckTop = deckTop,
        primaryDeckBottom = primaryDeckBottom,
        primaryDeckContentHeight = primaryDeckContentHeight,
        minimumInteractiveHeight = MinimumInteractiveHeight,
        requiresScroll = height < primaryDeckBottom,
        title = bounds(69f, 54f, 323f, 88f),
        medallion = bounds(26f, 104f, 364f, 413f),
        status = bounds(128f, 434f, 265f, 456f),
        activeProtocol = bounds(126f, 456f, 266f, 476f),
        phone = bounds(81f, 478f, 310f, 516f),
        contacts = bounds(34f, 511f, 356f, 594f),
        protocolArc = bounds(0f, 595f, 390f, 730f),
        buy = bounds(81f, 724f, 309f, 769f),
        bottomConsole = bottomConsole,
    )
}

private const val ReferenceWidth = 390f
private const val HeroTranslationY = -58f
private const val DeckTop = 434f
private const val MinimumInteractiveHeight = 48f

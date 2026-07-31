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
    val supportNote: PhoneHomeReferenceBounds,
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

    val deckTop = DeckTop * scale
    val primaryDeckContentHeight = PrimaryDeckContentHeight
    val primaryDeckBottom = deckTop + primaryDeckContentHeight
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
        status = bounds(128f, 363f, 265f, 386f),
        activeProtocol = bounds(126f, 386f, 266f, 406f),
        phone = bounds(81f, 407f, 310f, 445f),
        supportNote = bounds(84f, 446f, 306f, 484f),
        contacts = bounds(34f, 486f, 356f, 569f),
        protocolArc = bounds(0f, 570f, 390f, 705f),
        buy = bounds(81f, 699f, 309f, 744f),
        bottomConsole = bounds(8f, 735f, 382f, 839f),
    )
}

private const val ReferenceWidth = 390f
private const val HeroTranslationY = -58f
private const val DeckTop = 363f
private const val PrimaryDeckContentHeight = 476f
private const val MinimumInteractiveHeight = 48f

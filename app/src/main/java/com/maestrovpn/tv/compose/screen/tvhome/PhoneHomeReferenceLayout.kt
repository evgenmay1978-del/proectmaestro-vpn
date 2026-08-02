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
    val referenceScale: Float,
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

internal fun phoneHomeReferenceBounds(
    referenceScale: Float,
    left: Float,
    top: Float,
    right: Float,
    bottom: Float,
) = PhoneHomeReferenceBounds(
    left = left * referenceScale,
    top = top * referenceScale,
    right = right * referenceScale,
    bottom = bottom * referenceScale,
)

internal fun phoneHomeReferenceLayout(width: Float, height: Float): PhoneHomeReferenceLayout {
    require(width > 0f && height > 0f) { "Phone Home viewport dimensions must be positive" }

    val referenceScale = width / ReferenceWidth
    val bottomConsole = phoneHomeReferenceBounds(referenceScale, 8f, 760f, 382f, 864f)
    val deckTop = DeckTop * referenceScale
    val primaryDeckBottom = bottomConsole.bottom
    val primaryDeckContentHeight = primaryDeckBottom - deckTop
    return PhoneHomeReferenceLayout(
        heroScale = 1f,
        referenceScale = referenceScale,
        heroTranslationY = HeroTranslationY * referenceScale,
        deckTop = deckTop,
        primaryDeckBottom = primaryDeckBottom,
        primaryDeckContentHeight = primaryDeckContentHeight,
        minimumInteractiveHeight = MinimumInteractiveHeight,
        requiresScroll = height < primaryDeckBottom,
        title = phoneHomeReferenceBounds(referenceScale, 69f, 54f, 323f, 88f),
        medallion = phoneHomeReferenceBounds(referenceScale, 26f, 104f, 364f, 413f),
        status = phoneHomeReferenceBounds(referenceScale, 128f, 434f, 265f, 456f),
        activeProtocol = phoneHomeReferenceBounds(referenceScale, 126f, 456f, 266f, 476f),
        phone = phoneHomeReferenceBounds(referenceScale, 81f, 478f, 310f, 516f),
        contacts = phoneHomeReferenceBounds(referenceScale, 34f, 511f, 356f, 594f),
        protocolArc = phoneHomeReferenceBounds(referenceScale, 0f, 595f, 390f, 730f),
        buy = phoneHomeReferenceBounds(referenceScale, 81f, 724f, 309f, 769f),
        bottomConsole = bottomConsole,
    )
}

private const val ReferenceWidth = 390f
private const val HeroTranslationY = -58f
private const val DeckTop = 434f
private const val MinimumInteractiveHeight = 48f

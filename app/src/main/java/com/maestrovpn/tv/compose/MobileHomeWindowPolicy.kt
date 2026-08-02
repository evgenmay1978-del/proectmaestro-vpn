package com.maestrovpn.tv.compose

internal fun mobileHomeUsesFullWindow(
    isTelevision: Boolean,
    isHomeRoute: Boolean,
): Boolean = !isTelevision && isHomeRoute

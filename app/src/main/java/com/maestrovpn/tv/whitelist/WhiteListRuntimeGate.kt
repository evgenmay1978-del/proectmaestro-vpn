package com.maestrovpn.tv.whitelist

object WhiteListRuntimeGate {
    fun enabled(isTelevision: Boolean, model: WhiteListDisplayModel?): Boolean =
        !isTelevision && model?.runtimeEligible == true
}

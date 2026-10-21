package com.maestrovpn.tv.bg

/**
 * Which packages the tunnel carries.
 *
 * The app's own uid is ALWAYS kept outside the tunnel. The control plane — claim,
 * /sub refresh, panel update.json/APK download, purchase API, account card — must keep
 * working while the tunnel is up but dead. Before this policy the app put itself into the
 * tunnel, so a stale or broken config locked the app out of its only recovery path and the
 * user had to force-stop it (incident 21.10.2026: SEI KP1 on 1.0.168, every claim/sub request
 * died while the panel and CDN were healthy).
 *
 * Pure on purpose: the full mode matrix is unit-tested without Android.
 */
internal object PackagePolicy {
    data class Decision(val include: List<String>, val exclude: List<String>) {
        init {
            require(include.isEmpty() || exclude.isEmpty()) {
                "Android allows either the include or the exclude list, never both"
            }
        }
    }

    fun decide(
        appPackage: String,
        perAppEnabled: Boolean,
        includeMode: Boolean,
        appList: Set<String>,
    ): Decision {
        require(appPackage.isNotBlank()) { "app package is required" }
        // A package list that still names the app cannot tunnel it: it is always removed.
        val others = (appList - appPackage).sorted()
        return when {
            // Include mode: only listed apps are tunneled; our app is simply not listed.
            perAppEnabled && includeMode && others.isNotEmpty() ->
                Decision(include = others, exclude = emptyList())
            // Include mode with nothing selected would tunnel every app — invert to exclude-self.
            perAppEnabled && includeMode -> Decision(include = emptyList(), exclude = listOf(appPackage))
            // Exclude mode: everything is tunneled except the user's bypass list and ourselves.
            perAppEnabled -> Decision(include = emptyList(), exclude = (others + appPackage).sorted())
            // No per-app list: tunnel everything except the app itself.
            else -> Decision(include = emptyList(), exclude = listOf(appPackage))
        }
    }
}

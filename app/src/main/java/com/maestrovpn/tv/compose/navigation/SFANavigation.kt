package com.maestrovpn.tv.compose.navigation

import android.net.Uri
import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.core.tween
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.navArgument
import com.maestrovpn.tv.compose.screen.configuration.NewProfileScreen
import com.maestrovpn.tv.compose.screen.claim.ClaimScreen
import com.maestrovpn.tv.compose.screen.qrscan.ScanQrActivateScreen
import com.maestrovpn.tv.compose.screen.purchase.BuyScreen
import com.maestrovpn.tv.compose.screen.tvhome.IosKaringDialog
import com.maestrovpn.tv.compose.screen.tvhome.TvHomeScreen
import com.maestrovpn.tv.compose.screen.tvhome.rememberAccountInfo
import com.maestrovpn.tv.compose.screen.dashboard.DashboardViewModel
import com.maestrovpn.tv.compose.screen.dashboard.GroupsCard
import com.maestrovpn.tv.compose.screen.dashboard.groups.GroupsViewModel
import com.maestrovpn.tv.compose.screen.log.HookLogScreen
import com.maestrovpn.tv.compose.screen.log.LogScreen
import com.maestrovpn.tv.compose.screen.log.LogViewModel
import com.maestrovpn.tv.compose.screen.privilegesettings.PrivilegeSettingsManageScreen
import com.maestrovpn.tv.compose.screen.profile.EditProfileRoute
import com.maestrovpn.tv.compose.screen.profileoverride.PerAppProxyScreen
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.compose.screen.settings.AppSettingsScreen
import com.maestrovpn.tv.compose.screen.settings.CoreSettingsScreen
import com.maestrovpn.tv.compose.screen.settings.EditRemoteServerScreen
import com.maestrovpn.tv.compose.screen.settings.FDroidMirrorScreen
import com.maestrovpn.tv.compose.screen.settings.PrivilegeSettingsScreen
import com.maestrovpn.tv.compose.screen.settings.ProfileOverrideScreen
import com.maestrovpn.tv.compose.screen.settings.RemoteControlScreen
import com.maestrovpn.tv.compose.screen.settings.ServiceSettingsScreen
import com.maestrovpn.tv.compose.screen.settings.SettingsScreen
import com.maestrovpn.tv.compose.screen.settings.TailscaleFontPickerScreen
import com.maestrovpn.tv.compose.screen.settings.TailscaleTerminalConfigScreen
import com.maestrovpn.tv.compose.screen.settings.TailscaleThemePickerScreen
import com.maestrovpn.tv.constant.Status

private val slideInFromRight: AnimatedContentTransitionScope<*>.() -> androidx.compose.animation.EnterTransition = {
    slideIntoContainer(AnimatedContentTransitionScope.SlideDirection.Left, animationSpec = tween(300))
}

private val slideOutToRight: AnimatedContentTransitionScope<*>.() -> androidx.compose.animation.ExitTransition = {
    slideOutOfContainer(AnimatedContentTransitionScope.SlideDirection.Right, animationSpec = tween(300))
}

private val slideInFromLeft: AnimatedContentTransitionScope<*>.() -> androidx.compose.animation.EnterTransition = {
    slideIntoContainer(AnimatedContentTransitionScope.SlideDirection.Right, animationSpec = tween(300))
}

private val slideOutToLeft: AnimatedContentTransitionScope<*>.() -> androidx.compose.animation.ExitTransition = {
    slideOutOfContainer(AnimatedContentTransitionScope.SlideDirection.Left, animationSpec = tween(300))
}

@Composable
fun SFANavHost(
    navController: NavHostController,
    serviceStatus: Status = Status.Stopped,
    showStartFab: Boolean = false,
    showStatusBar: Boolean = false,
    newProfileArgs: NewProfileArgs = NewProfileArgs(),
    onClearNewProfileArgs: () -> Unit = {},
    onOpenNewProfile: (NewProfileArgs) -> Unit = {},
    dashboardViewModel: DashboardViewModel? = null,
    logViewModel: LogViewModel? = null,
    groupsViewModel: GroupsViewModel? = null,
    modifier: Modifier = Modifier,
) {
    NavHost(
        navController = navController,
        startDestination = Screen.TvHome.route,
        modifier = modifier,
    ) {
        composable(Screen.TvHome.route) {
            val tvStatusText = when (serviceStatus) {
                Status.Starting -> "Подключение…"
                Status.Started -> "Подключено"
                else -> "Отключено"
            }
            val connected = serviceStatus == Status.Started || serviceStatus == Status.Starting
            // login + days-left for the active subscription (refetched on connect change)
            val accountInfo by rememberAccountInfo(connected)
            var showIosQr by remember { mutableStateOf(false) }
            if (showIosQr) {
                IosKaringDialog(onDismiss = { showIosQr = false })
            }
            if (groupsViewModel != null) {
                val groupsUi = groupsViewModel.uiState.collectAsState().value
                LaunchedEffect(serviceStatus) { groupsViewModel.updateServiceStatus(serviceStatus) }
                // the manual "select" group lists the protocol outbounds (auto/vless/hysteria2/…)
                val selectGroup = groupsUi.groups.firstOrNull { it.tag == "select" }
                    ?: groupsUi.groups.firstOrNull { it.selectable }
                // Resolve the protocol ACTUALLY carrying traffic: follow the
                // selector → urltest → … → leaf chain. With "auto", the urltest
                // group's `selected` is the live lowest-latency pick, so this
                // updates whenever auto switches protocol.
                var activeProtocol: String? = selectGroup?.selected
                var hop = 0
                while (activeProtocol != null && hop++ < 5) {
                    val g = groupsUi.groups.firstOrNull { it.tag == activeProtocol } ?: break
                    activeProtocol = g.selected
                }
                TvHomeScreen(
                    statusText = tvStatusText,
                    connected = connected,
                    protocols = selectGroup?.items?.map { it.tag } ?: emptyList(),
                    selected = selectGroup?.selected,
                    activeProtocol = activeProtocol,
                    accountLogin = accountInfo.login,
                    daysLeft = accountInfo.daysLeft,
                    onToggleConnect = { dashboardViewModel?.toggleService() },
                    onSelectProtocol = { tag -> selectGroup?.let { groupsViewModel.selectGroupItem(it.tag, tag) } },
                    onBuy = { navController.navigate("buy") },
                    onEnterCode = { navController.navigate("claim") },
                    onSplitTunnel = { navController.navigate("split") },
                    onShareIos = { showIosQr = true },
                    onScanQr = { navController.navigate("scanqr") },
                )
            } else {
                TvHomeScreen(
                    statusText = tvStatusText,
                    connected = connected,
                    protocols = emptyList(),
                    selected = null,
                    accountLogin = accountInfo.login,
                    daysLeft = accountInfo.daysLeft,
                    onToggleConnect = { dashboardViewModel?.toggleService() },
                    onSelectProtocol = {},
                    onBuy = { navController.navigate("buy") },
                    onEnterCode = { navController.navigate("claim") },
                    onSplitTunnel = { navController.navigate("split") },
                    onShareIos = { showIosQr = true },
                    onScanQr = { navController.navigate("scanqr") },
                )
            }
        }

        composable(
            "claim",
            enterTransition = slideInFromRight, exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft, popExitTransition = slideOutToRight,
        ) {
            ClaimScreen(onDone = { navController.popBackStack() })
        }

        composable(
            "scanqr",
            enterTransition = slideInFromRight, exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft, popExitTransition = slideOutToRight,
        ) {
            ScanQrActivateScreen(onDone = { navController.popBackStack() })
        }

        composable(
            "buy",
            enterTransition = slideInFromRight, exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft, popExitTransition = slideOutToRight,
        ) {
            BuyScreen(onDone = { navController.popBackStack() })
        }

        composable(
            "split",
            enterTransition = slideInFromRight, exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft, popExitTransition = slideOutToRight,
        ) {
            // Per-app split tunnel (donor screen). On exit, enable split only when
            // the user actually picked apps; an empty list means "all apps via VPN".
            PerAppProxyScreen(
                onBack = {
                    Settings.perAppProxyEnabled = Settings.perAppProxyList.isNotEmpty()
                    navController.popBackStack()
                },
                serviceStatus = serviceStatus,
            )
        }

        composable(Screen.Log.route) {
            if (logViewModel != null) {
                LogScreen(
                    serviceStatus = serviceStatus,
                    showStartFab = showStartFab,
                    showStatusBar = showStatusBar,
                    viewModel = logViewModel,
                )
            } else {
                LogScreen(
                    serviceStatus = serviceStatus,
                    showStartFab = showStartFab,
                    showStatusBar = showStatusBar,
                )
            }
        }

        composable(Screen.Groups.route) {
            if (groupsViewModel != null) {
                GroupsCard(
                    serviceStatus = serviceStatus,
                    viewModel = groupsViewModel,
                    showTopBar = true,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                GroupsCard(
                    serviceStatus = serviceStatus,
                    showTopBar = true,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }

        composable(ProfileRoutes.NewProfile) {
            DisposableEffect(Unit) {
                onDispose { onClearNewProfileArgs() }
            }
            NewProfileScreen(
                importName = newProfileArgs.importName,
                importUrl = newProfileArgs.importUrl,
                qrsData = newProfileArgs.qrsData,
                onNavigateBack = {
                    onClearNewProfileArgs()
                    navController.navigateUp()
                },
                onProfileCreated = { profileId ->
                    onClearNewProfileArgs()
                    navController.navigate(ProfileRoutes.editProfile(profileId)) {
                        popUpTo(ProfileRoutes.NewProfile) {
                            inclusive = true
                        }
                    }
                },
            )
        }

        composable(
            route = ProfileRoutes.EditProfile,
            arguments = listOf(
                navArgument("profileId") {
                    type = NavType.LongType
                },
            ),
        ) { backStackEntry ->
            val profileId = backStackEntry.arguments?.getLong("profileId") ?: -1L
            EditProfileRoute(
                profileId = profileId,
                onNavigateBack = { navController.navigateUp() },
                modifier = Modifier.fillMaxSize(),
            )
        }

        composable(Screen.Settings.route) {
            SettingsScreen(navController = navController)
        }

        // Settings subscreens with slide animations
        composable(
            route = "settings/app",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            AppSettingsScreen(navController = navController, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/fdroid_mirror",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            FDroidMirrorScreen(navController = navController)
        }

        composable(
            route = "settings/core",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToRight,
            popEnterTransition = slideInFromRight,
            popExitTransition = slideOutToRight,
        ) {
            CoreSettingsScreen(navController = navController)
        }

        composable(
            route = "settings/service",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            ServiceSettingsScreen(navController = navController, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/profile_override",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            ProfileOverrideScreen(navController = navController, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/profile_override/manage",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            PerAppProxyScreen(onBack = { navController.navigateUp() }, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/remote_control",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            RemoteControlScreen(navController = navController)
        }

        composable(
            route = "settings/remote_control/new",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            EditRemoteServerScreen(navController = navController)
        }

        composable(
            route = "settings/remote_control/edit/{serverId}",
            arguments = listOf(navArgument("serverId") { type = NavType.LongType }),
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) { backStackEntry ->
            val serverId = backStackEntry.arguments?.getLong("serverId") ?: -1L
            EditRemoteServerScreen(navController = navController, serverId = serverId)
        }

        composable(
            route = "settings/privilege",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            PrivilegeSettingsScreen(navController = navController, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/privilege/manage",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            PrivilegeSettingsManageScreen(onBack = { navController.navigateUp() }, serviceStatus = serviceStatus)
        }

        composable(
            route = "settings/tailscale/terminal_config",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            TailscaleTerminalConfigScreen(navController = navController)
        }

        composable(
            route = "settings/tailscale/theme_picker/{isDark}",
            arguments = listOf(navArgument("isDark") { type = NavType.StringType }),
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) { backStackEntry ->
            val isDarkStr = backStackEntry.arguments?.getString("isDark") ?: "false"
            TailscaleThemePickerScreen(navController = navController, isDark = isDarkStr == "true")
        }

        composable(
            route = "settings/tailscale/font_picker",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            TailscaleFontPickerScreen(navController = navController)
        }

        composable(
            route = "settings/privilege/logs",
            enterTransition = slideInFromRight,
            exitTransition = slideOutToLeft,
            popEnterTransition = slideInFromLeft,
            popExitTransition = slideOutToRight,
        ) {
            HookLogScreen(onBack = { navController.navigateUp() })
        }
    }
}

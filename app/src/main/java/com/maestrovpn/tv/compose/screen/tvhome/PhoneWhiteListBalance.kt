package com.maestrovpn.tv.compose.screen.tvhome

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.ProcessLifecycleOwner
import androidx.preference.PreferenceDataStore
import com.maestrovpn.tv.bg.UpdateProfileWork
import com.maestrovpn.tv.constant.SettingsKey
import com.maestrovpn.tv.database.ProfileManager
import com.maestrovpn.tv.database.Settings
import com.maestrovpn.tv.database.preference.OnPreferenceDataStoreChangeListener
import com.maestrovpn.tv.whitelist.WhiteListBalance
import com.maestrovpn.tv.whitelist.WhiteListBalanceClient
import com.maestrovpn.tv.whitelist.displayText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Share account selection with /info; never display another imported account's wallet. */
@Composable
internal fun rememberPhoneAccountKey(): State<Pair<Long, Int>> {
    val selected = remember { mutableStateOf(Settings.selectedProfile to 0) }
    val scope = rememberCoroutineScope()
    DisposableEffect(Unit) {
        val listener = object : OnPreferenceDataStoreChangeListener {
            override fun onPreferenceDataStoreChanged(store: PreferenceDataStore, key: String) {
                if (key == SettingsKey.SELECTED_PROFILE) {
                    scope.launch { selected.value = Settings.selectedProfile to selected.value.second }
                }
            }
        }
        val profileChanged: () -> Unit = {
            scope.launch { selected.value = Settings.selectedProfile to selected.value.second + 1 }
            Unit
        }
        Settings.dataStore.registerChangeListener(listener)
        ProfileManager.registerCallback(profileChanged)
        selected.value = Settings.selectedProfile to selected.value.second
        onDispose {
            Settings.dataStore.unregisterChangeListener(listener)
            ProfileManager.unregisterCallback(profileChanged)
        }
    }
    return selected
}

/** Wallet and fetch status are separate from transport availability. */
internal data class PhoneCdnAccount(
    val balance: WhiteListBalance? = null,
    val loading: Boolean = true,
    val unavailable: Boolean = false,
) {
    val text: String?
        get() = balance?.displayText() ?: if (unavailable) "CDN: остаток временно недоступен" else null
}

@Composable
internal fun rememberPhoneWhiteListBalance(refreshKey: Any?): State<String?> {
    val account = rememberPhoneCdnAccount(refreshKey)
    return remember(account) { androidx.compose.runtime.derivedStateOf { account.value.text } }
}

@Composable
internal fun rememberPhoneCdnAccount(refreshKey: Any?): State<PhoneCdnAccount> {
    val accountSelection = rememberPhoneAccountKey()
    val accountKey = accountSelection.value
    val selectedProfileId = accountKey.first
    val lifecycle = remember { ProcessLifecycleOwner.get().lifecycle }
    var foreground by remember { mutableStateOf(lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) }
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, _ -> foreground = lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED) }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    return key(accountKey) {
        produceState(initialValue = PhoneCdnAccount(), foreground, refreshKey) {
            if (!foreground || selectedProfileId < 0L) {
                value = value.copy(loading = false)
                return@produceState
            }
            value = value.copy(loading = true)
            while (isActive) {
                val balance = try {
                    withContext(Dispatchers.IO) {
                        val url = ProfileManager.get(selectedProfileId)?.typed?.remoteURL
                        if (url == null || !UpdateProfileWork.isTrustedSubUrl(url)) null
                        else WhiteListBalanceClient.fetch(url)
                    }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) { null }
                coroutineContext.ensureActive()
                if (Settings.selectedProfile != selectedProfileId || accountSelection.value != accountKey) {
                    value = PhoneCdnAccount(loading = false)
                    return@produceState
                }
                // A failed fetch never becomes zero and never takes the user to checkout.
                value = if (balance == null) value.copy(loading = false, unavailable = true)
                    else PhoneCdnAccount(balance = balance, loading = false)
                delay(30_000L)
            }
        }
    }
}

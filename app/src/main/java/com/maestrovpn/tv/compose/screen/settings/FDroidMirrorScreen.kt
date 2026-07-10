package com.maestrovpn.tv.compose.screen.settings

import android.webkit.URLUtil
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Speed
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import io.nekohasekai.libbox.Libbox
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyListRow
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.fantasy.FantasyTextField
import com.maestrovpn.tv.compose.theme.GoldHi
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.database.Settings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class MirrorEntry(
    val url: String,
    val name: String,
    val country: String,
    val isCustom: Boolean = false,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FDroidMirrorScreen(navController: NavController) {
    OverrideTopBar {
        // ── oak top bar + Playfair gold title ──
        TopAppBar(
            title = {
                Text(
                    stringResource(R.string.fdroid_mirror),
                    fontFamily = PlayfairFamily,
                    fontWeight = FontWeight.Bold,
                    color = Color(0xFFE8C877),
                    letterSpacing = 1.sp,
                )
            },
            navigationIcon = {
                IconButton(onClick = { navController.navigateUp() }) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = stringResource(R.string.content_description_back),
                        tint = GoldHi,
                    )
                }
            },
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = Color(0xFF17110A),
                titleContentColor = Color(0xFFE8C877),
            ),
        )
    }

    val scope = rememberCoroutineScope()
    var selectedMirrorUrl by remember { mutableStateOf(Settings.fdroidMirrorUrl) }
    var isTesting by remember { mutableStateOf(false) }
    val latencyResults = remember { mutableStateMapOf<String, Int>() }
    val latencyErrors = remember { mutableStateMapOf<String, Boolean>() }
    var showAddForm by remember { mutableStateOf(false) }
    var newMirrorName by remember { mutableStateOf("") }
    var newMirrorUrl by remember { mutableStateOf("") }
    var urlError by remember { mutableStateOf<String?>(null) }
    val invalidUrlMessage = stringResource(R.string.fdroid_mirror_invalid_url)
    var customMirrors by remember { mutableStateOf(Settings.fdroidCustomMirrors) }

    val builtinMirrors = remember {
        val mirrors = mutableListOf<MirrorEntry>()
        val iter = Libbox.getFDroidMirrors()
        while (iter.hasNext()) {
            val m = iter.next()
            mirrors.add(MirrorEntry(url = m.url, name = m.name, country = m.country))
        }
        mirrors
    }

    val parsedCustomMirrors = remember(customMirrors) {
        customMirrors.map { entry ->
            val parts = entry.split("|", limit = 2)
            if (parts.size == 2) {
                MirrorEntry(url = parts[1], name = parts[0], country = "", isCustom = true)
            } else {
                MirrorEntry(url = entry, name = entry, country = "", isCustom = true)
            }
        }
    }

    val allMirrors = builtinMirrors + parsedCustomMirrors

    fun selectMirror(url: String) {
        selectedMirrorUrl = url
        Settings.fdroidMirrorUrl = url
    }

    fun testAllMirrors() {
        isTesting = true
        latencyResults.clear()
        latencyErrors.clear()
        scope.launch {
            allMirrors.map { mirror ->
                async(Dispatchers.IO) {
                    val r = Libbox.pingFDroidMirror(mirror.url)
                    withContext(Dispatchers.Main) {
                        if (r.latencyMs < 0) {
                            latencyErrors[r.url] = true
                        } else {
                            latencyResults[r.url] = r.latencyMs
                        }
                    }
                }
            }.awaitAll()
            val fastest = latencyResults.minByOrNull { it.value }
            if (fastest != null) {
                selectMirror(fastest.key)
            }
            isTesting = false
        }
    }

    fun deleteCustomMirror(mirror: MirrorEntry) {
        val encoded = "${mirror.name}|${mirror.url}"
        val newSet = customMirrors.toMutableSet()
        newSet.remove(encoded)
        customMirrors = newSet
        Settings.fdroidCustomMirrors = newSet
        if (selectedMirrorUrl == mirror.url) {
            selectMirror("https://f-droid.org/repo")
        }
    }

    fun addCustomMirror() {
        val url = newMirrorUrl.trim().trimEnd('/')
        if (!URLUtil.isHttpsUrl(url)) {
            urlError = invalidUrlMessage
            return
        }
        val name = newMirrorName.trim().ifEmpty { url }
        val encoded = "$name|$url"
        val newSet = customMirrors.toMutableSet()
        newSet.add(encoded)
        customMirrors = newSet
        Settings.fdroidCustomMirrors = newSet
        newMirrorName = ""
        newMirrorUrl = ""
        urlError = null
        showAddForm = false
    }

    val grouped = remember(builtinMirrors) {
        builtinMirrors.groupBy { it.country }
    }
    val countryOrder = remember(grouped) { grouped.keys.toList() }

    // ══════════════ Dark-Fantasy ══════════════
    FantasyScreenBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            GlossyButton(
                label = if (isTesting) {
                    stringResource(R.string.fdroid_mirror_testing)
                } else {
                    stringResource(R.string.fdroid_mirror_test_all)
                },
                onClick = { if (!isTesting) testAllMirrors() },
                accent = NeonGreen,
                icon = if (isTesting) null else Icons.Outlined.Speed,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )

            countryOrder.forEach { country ->
                val mirrors = grouped[country] ?: return@forEach

                SectionLabel(country.uppercase(), wood = true)

                mirrors.forEach { mirror ->
                    FantasyListRow(
                        title = mirror.name,
                        icon = null,
                        onClick = { selectMirror(mirror.url) },
                        trailing = {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                FantasyLatencyBadge(
                                    url = mirror.url,
                                    latencyResults = latencyResults,
                                    latencyErrors = latencyErrors,
                                )
                                Spacer(Modifier.width(10.dp))
                                SelectionDot(selected = selectedMirrorUrl == mirror.url)
                            }
                        },
                    )
                }
            }

            Spacer(Modifier.height(6.dp))
            SectionLabel(stringResource(R.string.fdroid_mirror_custom).uppercase(), wood = true)

            parsedCustomMirrors.forEach { mirror ->
                FantasyListRow(
                    title = mirror.name,
                    subtitle = mirror.url,
                    icon = null,
                    onClick = { selectMirror(mirror.url) },
                    trailing = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            FantasyLatencyBadge(
                                url = mirror.url,
                                latencyResults = latencyResults,
                                latencyErrors = latencyErrors,
                            )
                            Spacer(Modifier.width(6.dp))
                            IconButton(onClick = { deleteCustomMirror(mirror) }) {
                                Icon(
                                    imageVector = Icons.Outlined.Delete,
                                    contentDescription = stringResource(R.string.fdroid_mirror_delete),
                                    tint = Color(0xFFE5484D),
                                )
                            }
                            Spacer(Modifier.width(4.dp))
                            SelectionDot(selected = selectedMirrorUrl == mirror.url)
                        }
                    },
                )
            }

            if (showAddForm) {
                FantasyTextField(
                    value = newMirrorName,
                    onValueChange = { newMirrorName = it },
                    placeholder = stringResource(R.string.fdroid_mirror_name_hint),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                FantasyTextField(
                    value = newMirrorUrl,
                    onValueChange = {
                        newMirrorUrl = it
                        urlError = null
                    },
                    placeholder = stringResource(R.string.fdroid_mirror_url_hint),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                urlError?.let {
                    Text(it, color = Color(0xFFE5484D), fontSize = 13.sp)
                }
                GlossyButton(
                    label = stringResource(R.string.fdroid_mirror_add_action),
                    onClick = { addCustomMirror() },
                    accent = NeonGreen,
                    wood = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            } else {
                FantasyListRow(
                    title = stringResource(R.string.fdroid_mirror_add),
                    icon = Icons.Outlined.Add,
                    onClick = { showAddForm = true },
                )
            }

            Spacer(Modifier.height(16.dp))
        }
    }
}

/** Emerald filled / hollow bronze selection dot — the fantasy "radio" for a mirror row (phone). */
@Composable
private fun SelectionDot(selected: Boolean) {
    if (selected) {
        Box(
            Modifier
                .size(16.dp)
                .clip(CircleShape)
                .background(NeonGreen),
        )
    } else {
        Box(
            Modifier
                .size(16.dp)
                .clip(CircleShape)
                .border(1.5.dp, GoldMid.copy(alpha = 0.7f), CircleShape),
        )
    }
}

/** Latency badge styled for the Dark-Fantasy rows (phone). */
@Composable
private fun FantasyLatencyBadge(
    url: String,
    latencyResults: Map<String, Int>,
    latencyErrors: Map<String, Boolean>,
) {
    val latency = latencyResults[url]
    val failed = latencyErrors[url] == true
    when {
        latency != null -> {
            Text(
                text = stringResource(R.string.fdroid_mirror_latency, latency),
                fontSize = 12.sp,
                fontWeight = FontWeight.Medium,
                color = when {
                    latency < 100 -> NeonGreen
                    latency < 500 -> GoldMid
                    else -> Color(0xFFE5484D)
                },
            )
        }
        failed -> {
            Text(
                text = stringResource(R.string.fdroid_mirror_failed),
                fontSize = 12.sp,
                fontWeight = FontWeight.Medium,
                color = Color(0xFFE5484D),
            )
        }
        else -> {}
    }
}

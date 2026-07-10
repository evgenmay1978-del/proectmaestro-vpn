package com.maestrovpn.tv.compose.screen.settings

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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.component.GlossyButton
import com.maestrovpn.tv.compose.component.SectionLabel
import com.maestrovpn.tv.compose.fantasy.FantasyScreenBackground
import com.maestrovpn.tv.compose.fantasy.FantasyTextField
import com.maestrovpn.tv.compose.fantasy.fantasyFrame
import com.maestrovpn.tv.compose.theme.GoldMid
import com.maestrovpn.tv.compose.theme.NeonGreen
import com.maestrovpn.tv.compose.theme.PlayfairFamily
import com.maestrovpn.tv.compose.topbar.OverrideTopBar
import com.maestrovpn.tv.database.RemoteServer
import com.maestrovpn.tv.database.RemoteServerManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EditRemoteServerScreen(navController: NavController, serverId: Long = -1L) {
    val isNewServer = serverId == -1L

    OverrideTopBar {
        TopAppBar(
            title = {
                Text(
                    stringResource(
                        if (isNewServer) {
                            R.string.remote_new_server
                        } else {
                            R.string.remote_edit_server
                        },
                    ),
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
                        tint = GoldMid,
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
    var origin by remember { mutableStateOf<RemoteServer?>(null) }
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf("") }
    var secretVisible by remember { mutableStateOf(false) }
    var urlError by remember { mutableStateOf(false) }
    var isLoading by remember { mutableStateOf(!isNewServer) }

    LaunchedEffect(serverId) {
        if (!isNewServer) {
            val server = withContext(Dispatchers.IO) { RemoteServerManager.get(serverId) }
            if (server == null) {
                navController.navigateUp()
                return@LaunchedEffect
            }
            origin = server
            name = server.name
            url = server.url
            secret = server.secret
            isLoading = false
        }
    }

    if (isLoading) {
        return
    }

    // Shared save callback — identical logic for both UIs.
    val onSave: () -> Unit = {
        val validatedURL = RemoteServer.validateURL(url)
        if (validatedURL == null) {
            urlError = true
        } else {
            scope.launch(Dispatchers.IO) {
                val server = origin ?: RemoteServer()
                server.name = name.trim()
                server.url = validatedURL
                server.secret = secret
                if (origin != null) {
                    RemoteServerManager.update(server)
                } else {
                    RemoteServerManager.create(server)
                }
                withContext(Dispatchers.Main) {
                    navController.navigateUp()
                }
            }
        }
    }

    // ─────────────── Dark-Fantasy ───────────────
    FantasyScreenBackground {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            // ── Имя ──
            SectionLabel(stringResource(R.string.profile_name).uppercase(), wood = true)
            FantasyTextField(
                value = name,
                onValueChange = { name = it },
                placeholder = stringResource(R.string.remote_optional),
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )

            // ── URL ──
            SectionLabel(stringResource(R.string.profile_url).uppercase(), wood = true)
            FantasyTextField(
                value = url,
                onValueChange = {
                    url = it
                    urlError = false
                },
                placeholder = stringResource(R.string.profile_input_required),
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
            if (urlError) {
                Text(
                    text = stringResource(R.string.remote_invalid_url, url),
                    color = Color(0xFFE5484D),
                    fontSize = 14.sp,
                )
            }

            // ── Секрет (с переключателем видимости) ──
            SectionLabel(stringResource(R.string.remote_secret).uppercase(), wood = true)
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                FantasyTextField(
                    value = secret,
                    onValueChange = { secret = it },
                    placeholder = stringResource(R.string.remote_optional),
                    singleLine = true,
                    visualTransformation =
                    if (secretVisible) {
                        VisualTransformation.None
                    } else {
                        PasswordVisualTransformation()
                    },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    modifier = Modifier.weight(1f),
                )
                Spacer(Modifier.width(10.dp))
                Box(
                    modifier = Modifier
                        .size(60.dp)
                        .fantasyFrame(R.drawable.frame_button),
                    contentAlignment = Alignment.Center,
                ) {
                    IconButton(onClick = { secretVisible = !secretVisible }) {
                        Icon(
                            imageVector =
                            if (secretVisible) {
                                Icons.Default.VisibilityOff
                            } else {
                                Icons.Default.Visibility
                            },
                            contentDescription = null,
                            tint = GoldMid,
                        )
                    }
                }
            }

            Spacer(Modifier.height(8.dp))
            GlossyButton(
                label = stringResource(R.string.save),
                onClick = onSave,
                accent = NeonGreen,
                wood = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(16.dp))
        }
    }
}

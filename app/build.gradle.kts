import org.gradle.api.file.DuplicatesStrategy
import org.gradle.api.logging.Logger
import org.gradle.api.tasks.Sync
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.tasks.KotlinCompile
import java.io.ByteArrayInputStream
import java.io.FileInputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.Base64
import java.util.Properties
import java.util.zip.ZipFile

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.parcelize")
    id("com.google.devtools.ksp")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
    alias(libs.plugins.spotless)
}

fun getProps(propName: String): String {
    val propsInEnv = System.getenv("LOCAL_PROPERTIES")
    if (propsInEnv != null) {
        val props = Properties()
        props.load(ByteArrayInputStream(Base64.getDecoder().decode(propsInEnv)))
        val value = props.getProperty(propName)
        if (value != null) {
            return value
        }
    }
    val propsFile = rootProject.file("local.properties")
    if (propsFile.exists()) {
        val props = Properties()
        props.load(FileInputStream(propsFile))
        val value = props.getProperty(propName)
        if (value != null) {
            return value
        }
    }
    return ""
}

fun getVersionProps(propName: String): String {
    val propsFile = rootProject.file("version.properties")
    if (propsFile.exists()) {
        val props = Properties()
        props.load(FileInputStream(propsFile))
        val value = props.getProperty(propName)
        if (value != null) {
            return value
        }
    }
    return ""
}

val productionVersionCode = getVersionProps("VERSION_CODE").toInt()
val productionVersionName = getVersionProps("VERSION_NAME")
val task7ProductionBaselineVersionCode = 157
val task7ProductionBaselineVersionName = "1.0.157"
val task7TestVersionNameProperty = providers.gradleProperty("maestroTask7TestVersionName").orNull
val task7TestVersionCodeProperty = providers.gradleProperty("maestroTask7TestVersionCode").orNull
val task7TestOverrideActive =
    task7TestVersionNameProperty != null || task7TestVersionCodeProperty != null

require((task7TestVersionNameProperty == null) == (task7TestVersionCodeProperty == null)) {
    "Task 7 test version name and code must be supplied together"
}

val task7TestVersionCode = task7TestVersionCodeProperty?.toIntOrNull()
require(task7TestVersionCodeProperty == null || task7TestVersionCode != null) {
    "Task 7 test version code must be a bounded integer"
}

if (task7TestOverrideActive) {
    val task7TestVersionName = requireNotNull(task7TestVersionNameProperty)
    val task7TestVersionCodeValue = requireNotNull(task7TestVersionCode)
    require(task7TestVersionName.length in 1..64) {
        "Task 7 test version name is out of bounds"
    }
    require(Regex("""[0-9]+\.[0-9]+\.[0-9]+-task7-test""").matches(task7TestVersionName)) {
        "Task 7 test version name must end with -task7-test"
    }
    require(task7TestVersionName == "1.0.158-task7-test") {
        "Task 7 test version name must be 1.0.158-task7-test"
    }
    require(task7TestVersionCodeValue == 1015800) {
        "Task 7 test version code must be 1015800"
    }
    require(task7TestVersionName != productionVersionName) {
        "Task 7 test version name must differ from source defaults"
    }
    require(task7TestVersionName != task7ProductionBaselineVersionName) {
        "Task 7 test version name must differ from release baseline"
    }
    require(task7TestVersionCodeValue > productionVersionCode) {
        "Task 7 test version code must be above source defaults"
    }
    require(task7TestVersionCodeValue > task7ProductionBaselineVersionCode) {
        "Task 7 test version code must be above release baseline"
    }
}

val releaseKeystorePassword = getProps("KEYSTORE_PASS")
val releaseKeyAlias = getProps("ALIAS_NAME")
val releaseKeyPassword = getProps("ALIAS_PASS")
val releaseSigningConfigured =
    listOf(releaseKeystorePassword, releaseKeyAlias, releaseKeyPassword).any { it.isNotEmpty() }
if (task7TestOverrideActive) {
    require(!releaseSigningConfigured) {
        "Task 7 test APK must not use release signing"
    }
}

val cdnApkProperty = providers.gradleProperty("maestroCdnApk").orNull
require(cdnApkProperty == null || cdnApkProperty == "true") { "maestroCdnApk must be true when supplied" }
val cdnApkBuild = cdnApkProperty == "true"
val emulatorApkProperty = providers.gradleProperty("maestroEmulatorApk").orNull
require(emulatorApkProperty == null || emulatorApkProperty == "true")
val emulatorApkBuild = emulatorApkProperty == "true"
require(!emulatorApkBuild || cdnApkBuild) { "Emulator packaging requires the same signed APK configuration" }
if (cdnApkBuild) {
    require(!task7TestOverrideActive) { "CDN APK and Task 7 version overrides cannot be combined" }
    require(listOf(releaseKeystorePassword, releaseKeyAlias, releaseKeyPassword).all { it.isNotEmpty() } &&
        file("release.keystore").isFile) { "CDN APK requires the existing stable signing keystore and all signing properties" }
    require(1016011 > productionVersionCode) { "CDN APK version must exceed the source version" }
}

val effectiveVersionName = if (cdnApkBuild) "1.0.166" else task7TestVersionNameProperty ?: productionVersionName
val effectiveVersionCode = if (cdnApkBuild) 1016011 else task7TestVersionCode ?: productionVersionCode
android {
    namespace = "com.maestrovpn.tv"
    compileSdk = 36

    ndkVersion = "28.0.13004108"

    System.getenv("ANDROID_NDK_HOME")?.let { ndkPath = it }

    ksp {
        arg("room.incremental", "true")
        arg("room.schemaLocation", "${projectDir}/schemas")
    }

    defaultConfig {
        applicationId = "com.maestrovpn.tv"
        minSdk = 23
        targetSdk = 35
        versionCode = effectiveVersionCode
        versionName = effectiveVersionName
        base.archivesName.set("MaestroVPN-TV-$effectiveVersionName" + if (emulatorApkBuild) "-emulator" else "")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        // backend the TV app hits for the claim-code → subscription exchange
        buildConfigField("String", "BACKEND_URL", "\"https://wapmixx.ru:8911\"")
        // Ship only ARM ABIs. Every real RU Android-TV box / phone is arm64-v8a or
        // armeabi-v7a (old 32-bit boxes). x86/x86_64 are emulator-only and added
        // ~130MB of libbox.so, making a 138MB APK that flaked on mobile OTA ("то
        // ставится то нет"). Dropping them ~halves the download.
        ndk {
            abiFilters += if (emulatorApkBuild) listOf("x86_64") else listOf("arm64-v8a", "armeabi-v7a")
        }
    }

    signingConfigs {
        create("release") {
            storeFile = file("release.keystore")
            storePassword = releaseKeystorePassword
            keyAlias = releaseKeyAlias
            keyPassword = releaseKeyPassword
        }
    }

    buildTypes {
        debug {
            // The FLEET ships this build type (CI runs :app:assembleOtherDebug and publishes the
            // result as the OTA release), so it must not carry AGP's debug default of
            // debuggable=true: that flag lets anyone with adb `run-as` the app and read its private
            // data — profiles.db (sub tokens = full subscription access) and configs/*.json (server
            // creds) — with no root. Verified present in shipped 1.0.149 via `aapt dump badging`.
            //
            // SIDE EFFECT, by design: a non-debuggable variant counts as a "release target", so
            // AGP now also runs lintVitalOtherDebug on every build and release. It immediately
            // caught a long-standing invalid <exclude> in backup_rules.xml that had never been
            // checked. Keep it — it is the only automated gate this pipeline has — but expect a
            // release to fail if lint finds a new FATAL issue; fix the issue, don't silence it.
            isDebuggable = false
            if (releaseKeystorePassword.isNotEmpty()) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
        release {
            // R8 ВЫКЛЮЧЕН (18.10.2026): в R8-релизах 1.0.169–1.0.171 AWG поднимал туннель
            // (рукопожатие с S4 есть), но трафик не шёл — «подключено, интернета нет»; та же
            // кодовая база без минификации (тестовые сборки, флот ≤1.0.168) работает. Пока точная
            // причина в keep-правилах не найдена, релиз собирается как проверенная тестовая сборка.
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName("release")
            vcsInfo.include = false
        }
    }

    dependenciesInfo {
        includeInApk = false
    }

    flavorDimensions += "vendor"
    productFlavors {
        create("other") {
            minSdk = 23
        }
    }

    sourceSets {
        getByName("other") {
            java.directories.addAll(listOf("src/minApi23/java", "src/github/java"))
            aidl.directories.add("src/minApi23/aidl")
        }
    }

    splits {
        abi {
            // single universal APK for owner-sideload distribution (no per-ABI
            // confusion when installing on phones/TV boxes)
            isEnable = false
            isUniversalApk = true
            reset()
            include("armeabi-v7a", "arm64-v8a")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    androidResources {
        generateLocaleConfig = true
    }

    buildFeatures {
        viewBinding = true
        aidl = true
        compose = true
        buildConfig = true
    }

    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    lint {
        fatal += "NewApi"
    }

    applicationVariants.configureEach {
        outputs.configureEach {
            val output = this as com.android.build.gradle.internal.api.BaseVariantOutputImpl
            var fileName = output.outputFileName
            fileName = fileName.replace("-release", "")
            fileName = fileName.replace("-other", "")
            output.outputFileName = fileName
        }
    }
}

dependencies {
    // libbox
    "otherImplementation"(files("libs/libbox.aar"))

    // API level specific versions
    val lifecycleVersion23 = "2.10.0"
    val roomVersion23 = "2.8.4"
    val workVersion23 = "2.11.1"
    val cameraVersion23 = "1.5.3"
    val browserVersion23 = "1.9.0"


    // Common dependencies (no API level difference)
    implementation("androidx.core:core-ktx:1.17.0")
    implementation("androidx.appcompat:appcompat:1.7.1")
    implementation("com.google.android.material:material:1.13.0")
    implementation("androidx.constraintlayout:constraintlayout:2.2.1")
    implementation("androidx.navigation:navigation-fragment-ktx:2.9.7")
    implementation("androidx.navigation:navigation-ui-ktx:2.9.7")
    implementation("com.google.zxing:core:3.5.4")
    implementation("androidx.coordinatorlayout:coordinatorlayout:1.3.0")
    implementation("androidx.preference:preference-ktx:1.2.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.10.0")
    implementation("com.blacksquircle.ui:editorkit:2.2.0")
    implementation("com.blacksquircle.ui:language-json:2.2.0")
    implementation("com.android.tools.smali:smali-dexlib2:3.0.9") {
        exclude(group = "com.google.guava", module = "guava")
    }
    implementation("com.google.guava:guava:33.5.0-android")

    // API 23+ dependencies (play/other)

    "otherImplementation"("androidx.lifecycle:lifecycle-livedata-ktx:$lifecycleVersion23")
    "otherImplementation"("androidx.lifecycle:lifecycle-viewmodel-ktx:$lifecycleVersion23")
    "otherImplementation"("androidx.lifecycle:lifecycle-process:$lifecycleVersion23")
    "otherImplementation"("androidx.room:room-runtime:$roomVersion23")
    "otherImplementation"("androidx.work:work-runtime-ktx:$workVersion23")
    "otherImplementation"("androidx.camera:camera-view:$cameraVersion23")
    "otherImplementation"("androidx.camera:camera-lifecycle:$cameraVersion23")
    "otherImplementation"("androidx.camera:camera-camera2:$cameraVersion23")
    "otherImplementation"("androidx.browser:browser:$browserVersion23")
    "kspOther"("androidx.room:room-compiler:$roomVersion23")

    // API 21 dependencies (otherLegacy)

    // Play Store specific

    // Shizuku (play and other flavors, API 23+ only)
    val shizukuVersion = "12.2.0"
    "otherImplementation"("dev.rikka.shizuku:api:$shizukuVersion")
    "otherImplementation"("dev.rikka.shizuku:provider:$shizukuVersion")

    // libsu for ROOT package query (all flavors)
    val libsuVersion = "6.0.0"
    "otherImplementation"("com.github.topjohnwu.libsu:core:$libsuVersion")
    "otherImplementation"("com.github.topjohnwu.libsu:service:$libsuVersion")

    // Compose dependencies - API 23+ (play/other)
    val composeBom23 = platform("androidx.compose:compose-bom:2026.02.00")
    val activityVersion23 = "1.12.4"
    val lifecycleComposeVersion23 = "2.10.0"


    "otherImplementation"(composeBom23)
    "otherImplementation"("androidx.compose.material3:material3")
    "otherImplementation"("androidx.compose.material3.adaptive:adaptive")
    "otherImplementation"("androidx.compose.ui:ui")
    "otherImplementation"("androidx.compose.ui:ui-tooling-preview")
    "otherImplementation"("androidx.compose.material:material-icons-extended")
    "otherImplementation"("androidx.activity:activity-compose:$activityVersion23")
    "otherImplementation"("androidx.navigation:navigation-compose:2.9.7")
    "otherImplementation"("androidx.lifecycle:lifecycle-viewmodel-compose:$lifecycleComposeVersion23")
    "otherImplementation"("androidx.compose.runtime:runtime-livedata")

    // Compose dependencies - API 21 (otherLegacy)


    // Debug/Test dependencies
    testImplementation("junit:junit:4.13.2")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    "androidTestOtherImplementation"(composeBom23)
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")

    // Common Compose-related libraries
    implementation("sh.calvin.reorderable:reorderable:3.0.0")
    implementation("com.github.jeziellago:compose-markdown:0.5.8")
    implementation("org.kodein.emoji:emoji-kt:2.3.0")

    // Android TV (D-pad / leanback) Material components for the TV-first screens
    implementation("androidx.tv:tv-material:1.0.0")

}

// ── AIVPN (experimental) native core ─────────────────────────────────────────────────────────────
// libaivpn_core.so is a vendored binary and stays out of git (see .gitignore: app/src/main/jniLibs/).
// Every build takes it from the pinned AIVPN v1.2.0 release APK and checks the published sha256, so
// a tampered or half-downloaded artifact never reaches an APK. A failure here MUST NOT fail the
// build: without the library the app still compiles and ships the sing-box path, and
// AivpnJni.isAvailable simply reports false to the UI.
val aivpnReleaseUrl = "https://github.com/infosave2007/aivpn/releases/download/v1.2.0/aivpn-android.apk"
val aivpnReleaseSha256 = "45370f3d505fbdff71ba6c43c90444041ba52aaf64c80b68a2b22adcd0b5c4e9"
val aivpnReleaseBytes = 27_674_391L
val aivpnLibSha256 = mapOf(
    "arm64-v8a" to "5db4a55b0a83f0778dcb4c779e067455a89131aa0aa7ca00e509d744065959dd",
    "armeabi-v7a" to "64d769da3cc1ac520679db8a9dd0de8729d7b15d9a830132710f77a1f68738df",
)
val aivpnLibBytes = mapOf(
    "arm64-v8a" to 12_891_696L,
    "armeabi-v7a" to 8_394_632L,
)

fun sha256Hex(file: File): String? = try {
    val digest = MessageDigest.getInstance("SHA-256")
    file.inputStream().use { input ->
        val buffer = ByteArray(64 * 1024)
        var read = input.read(buffer)
        while (read >= 0) {
            digest.update(buffer, 0, read)
            read = input.read(buffer)
        }
    }
    val hash = digest.digest()
    val hex = StringBuilder(hash.size * 2)
    for (b in hash) {
        hex.append("%02x".format(b.toInt() and 0xff))
    }
    hex.toString()
} catch (e: java.io.IOException) {
    null
}

val aivpnJniLibsDir = file("src/main/jniLibs")
val aivpnCacheDir = layout.buildDirectory.dir("aivpn").get().asFile

// The work lives in an ordinary function on purpose: the Kotlin DSL script compiler rejects plain
// "if"/"try" statements inside a script-body lambda, and "java.net"/"java.util.zip" prefixes resolve
// to the script accessors instead of the JDK packages, so both are imported above.
fun fetchAivpnNativeLibs(jniLibsDir: File, cacheDir: File, log: Logger) {
    val missing = aivpnLibSha256.keys.filter { abi ->
        val target = File(jniLibsDir, "$abi/libaivpn_core.so")
        val okay = target.isFile &&
            target.length() == aivpnLibBytes.getValue(abi) &&
            sha256Hex(target) == aivpnLibSha256.getValue(abi)
        !okay
    }
    if (missing.isEmpty()) {
        log.lifecycle("AIVPN: libaivpn_core.so is already in place")
        return
    }
    try {
        val apk = File(cacheDir, "aivpn-android.apk")
        val cached = apk.isFile &&
            apk.length() == aivpnReleaseBytes &&
            sha256Hex(apk) == aivpnReleaseSha256
        if (!cached) {
            cacheDir.mkdirs()
            val temp = File(cacheDir, "aivpn-android.apk.part")
            log.lifecycle("AIVPN: downloading the pinned v1.2.0 release APK")
            val connection = URL(aivpnReleaseUrl).openConnection() as HttpURLConnection
            connection.connectTimeout = 20_000
            connection.readTimeout = 120_000
            connection.instanceFollowRedirects = true
            connection.inputStream.use { input ->
                temp.outputStream().use { output -> input.copyTo(output) }
            }
            val authentic = temp.length() == aivpnReleaseBytes &&
                sha256Hex(temp) == aivpnReleaseSha256
            if (!authentic) {
                temp.delete()
                throw IllegalStateException("the downloaded release APK does not match the pinned sha256")
            }
            if (!temp.renameTo(apk)) {
                temp.copyTo(apk, overwrite = true)
                temp.delete()
            }
        }
        ZipFile(apk).use { zip ->
            for (abi in missing) {
                val entry = zip.getEntry("lib/$abi/libaivpn_core.so")
                    ?: throw IllegalStateException("the release APK has no lib/$abi/libaivpn_core.so")
                val dir = File(jniLibsDir, abi)
                dir.mkdirs()
                val temp = File(dir, "libaivpn_core.so.part")
                zip.getInputStream(entry).use { input ->
                    temp.outputStream().use { output -> input.copyTo(output) }
                }
                val authentic = temp.length() == aivpnLibBytes.getValue(abi) &&
                    sha256Hex(temp) == aivpnLibSha256.getValue(abi)
                if (!authentic) {
                    temp.delete()
                    throw IllegalStateException("lib/$abi/libaivpn_core.so does not match the pinned sha256")
                }
                val target = File(dir, "libaivpn_core.so")
                if (!temp.renameTo(target)) {
                    temp.copyTo(target, overwrite = true)
                    temp.delete()
                }
                log.lifecycle("AIVPN: installed $abi/libaivpn_core.so")
            }
        }
    } catch (t: Throwable) {
        log.warn("AIVPN: native library not fetched (" + t.message + "); building without it")
    }
}

val aivpnFetchTask = tasks.register("fetchAivpnNativeLibs") {
    group = "build"
    description = "Fetches libaivpn_core.so (AIVPN v1.2.0) into src/main/jniLibs/<abi>"
    doLast {
        fetchAivpnNativeLibs(aivpnJniLibsDir, aivpnCacheDir, logger)
    }
}

// Both anchors are matched by name: AGP registers preBuild and the per-variant JniLibFolders merge
// late, so depending on them directly would fail at configuration time. The merge task is the one
// that actually collects src/main/jniLibs into the APK, preBuild keeps the plain "fetch first"
// contract for every other variant task.
tasks.matching { task ->
    task.name == "preBuild" || (task.name.startsWith("merge") && task.name.endsWith("JniLibFolders"))
}.configureEach {
    dependsOn(aivpnFetchTask)
}

tasks.withType<KotlinCompile>().configureEach {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

spotless {
    kotlin {
        target("src/**/*.kt")
        ktlint(libs.versions.ktlint.get())
            .editorConfigOverride(mapOf(
                "ktlint_standard_backing-property-naming" to "disabled",
                "ktlint_standard_filename" to "disabled",
                "ktlint_standard_max-line-length" to "disabled",
                "ktlint_standard_property-naming" to "disabled",
            ))
    }
    java {
        target("src/**/*.java")
        googleJavaFormat()
    }
}

// Engine gate (28.10.2026): SingBoxEngineGateTest asks the pinned sing-box binary whether the bytes
// this app hands over are accepted at all. Both are forwarded explicitly — with nothing forwarded
// the worker would see no engine and the gate would pass by skipping, which is exactly the hole the
// 404-green-unit-test release slipped through.
tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    systemProperty("sbx.bin", System.getenv("SINGBOX_BIN") ?: "")
    systemProperty("sbx.gate", (findProperty("sbxGate") as String?) ?: "")
}

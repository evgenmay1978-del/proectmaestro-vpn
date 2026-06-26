import org.gradle.api.file.DuplicatesStrategy
import org.gradle.api.tasks.Sync
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.tasks.KotlinCompile
import java.io.ByteArrayInputStream
import java.io.FileInputStream
import java.util.Base64
import java.util.Properties

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
        versionCode = getVersionProps("VERSION_CODE").toInt()
        versionName = getVersionProps("VERSION_NAME")
        base.archivesName.set("MaestroVPN-TV-${versionName}")
        // backend the TV app hits for the claim-code → subscription exchange
        buildConfigField("String", "BACKEND_URL", "\"https://wapmixx.ru:8911\"")
        // Ship only ARM ABIs. Every real RU Android-TV box / phone is arm64-v8a or
        // armeabi-v7a (old 32-bit boxes). x86/x86_64 are emulator-only and added
        // ~130MB of libbox.so, making a 138MB APK that flaked on mobile OTA ("то
        // ставится то нет"). Dropping them ~halves the download.
        ndk {
            abiFilters += listOf("arm64-v8a", "armeabi-v7a")
        }
    }

    signingConfigs {
        create("release") {
            storeFile = file("release.keystore")
            storePassword = getProps("KEYSTORE_PASS")
            keyAlias = getProps("ALIAS_NAME")
            keyPassword = getProps("ALIAS_PASS")
        }
    }

    buildTypes {
        debug {
            if (getProps("KEYSTORE_PASS").isNotEmpty()) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
        release {
            isMinifyEnabled = true
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
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    "androidTestOtherImplementation"(composeBom23)
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")

    // Common Compose-related libraries
    implementation("sh.calvin.reorderable:reorderable:3.0.0")
    implementation("com.github.jeziellago:compose-markdown:0.5.8")
    implementation("org.kodein.emoji:emoji-kt:2.3.0")

    // Android TV (D-pad / leanback) Material components for the TV-first screens
    implementation("androidx.tv:tv-material:1.0.0")

    // Terminal emulator

    // Xposed API for self-hooking VPN hide module
    compileOnly("de.robv.android.xposed:api:82")
    compileOnly(project(":libxposed-api"))
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

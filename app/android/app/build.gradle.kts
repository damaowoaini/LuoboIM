import java.security.MessageDigest

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Opt-in local QA trust material. It is never copied into source or release
// resources, and an ordinary debug build retains only platform system trust.
val fixtureCa = providers.gradleProperty("luoboFixtureCa").orNull?.takeIf { it.isNotBlank() }
val fixtureResources = layout.buildDirectory.dir("generated/luoboFixtureResources")
val prepareFixtureResources = if (fixtureCa != null) tasks.register("prepareLuoboFixtureResources") {
    inputs.file(fixtureCa)
    outputs.dir(fixtureResources)
    doLast {
        val bytes = file(fixtureCa).readBytes()
        val root = fixtureResources.get().asFile
        root.deleteRecursively()
        root.resolve("raw").mkdirs()
        root.resolve("xml").mkdirs()
        root.resolve("values").mkdirs()
        root.resolve("raw/luobo_fixture_ca.pem").writeBytes(bytes)
        val hash = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        root.resolve("values/fixture.xml").writeText("<resources><string name=\"luobo_fixture_ca_sha256\">$hash</string></resources>")
        root.resolve("xml/luobo_fixture_network_security.xml").writeText("""<network-security-config>
          <base-config cleartextTrafficPermitted="false"><trust-anchors>
            <certificates src="system"/><certificates src="@raw/luobo_fixture_ca"/>
          </trust-anchors></base-config></network-security-config>""")
    }
} else null

android {
    namespace = "com.luoboduihua.imapp"
    compileSdk = 37
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        manifestPlaceholders["luoboDebugNetworkSecurityConfig"] = if (fixtureCa != null) "@xml/luobo_fixture_network_security" else "@xml/luobo_debug_system_trust"
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.luoboduihua.imapp"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    buildTypes {
        release {
            // Production signing is supplied by the release pipeline. Never
            // silently ship a release signed with the development debug key.
        }
    }
    // The generation dependency is explicit below. AGP 9 disallows Provider
    // instances in this legacy SourceSet API, so resolve its directory here.
    if (fixtureCa != null) sourceSets.getByName("debug").res.srcDir(fixtureResources.get().asFile)
}

if (prepareFixtureResources != null) {
    tasks.matching { it.name == "preDebugBuild" }.configureEach { dependsOn(prepareFixtureResources) }
    gradle.taskGraph.whenReady {
        check(allTasks.none { it.project == project && it.name.contains("Release", ignoreCase = true) }) {
            "Fixture trust is debug-only; remove luoboFixtureCa for release builds"
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

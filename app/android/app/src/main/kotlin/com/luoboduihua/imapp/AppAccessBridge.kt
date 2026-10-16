package com.luoboduihua.imapp

import android.app.KeyguardManager
import android.hardware.biometrics.BiometricManager
import android.hardware.biometrics.BiometricPrompt
import android.os.Build
import android.os.CancellationSignal
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.view.View
import android.view.WindowManager
import android.widget.FrameLayout
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.security.KeyStore
import java.security.SecureRandom
import java.util.concurrent.Executors
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.Mac
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec

/** Separate local-access namespace. Never reads, migrates or resets IM tokens. */
class AppAccessBridge(private val activity: MainActivity, messenger: BinaryMessenger) : MethodChannel.MethodCallHandler {
    private val channel = MethodChannel(messenger, "luobo/app_access")
    private val worker = Executors.newSingleThreadExecutor()
    private val prefs = activity.getSharedPreferences("luobo.app_access.challenge.v1", 0)
    private var cancellation: CancellationSignal? = null
    private var pending: MethodChannel.Result? = null
    private var pendingRequest: Int? = null
    private var pendingOwner: String? = null
    private var pendingEnroll = false
    private var privacyEnabled = false
    private var secureAdded = false
    private var cover: View? = null
    init { channel.setMethodCallHandler(this) }

    override fun onMethodCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "capabilities" -> result.success(try { available() } catch (_: Exception) { false })
            "derivePin" -> derive(call, result)
            "enroll", "authenticate" -> authenticate(call, result, call.method == "enroll")
            "cancel" -> { cancel(); result.success(null) }
            "remove" -> {
                val owner = owner(call) ?: return result.error("invalid", null, null)
                try { remove(owner); result.success(null) } catch (_: Exception) { result.error("storage", null, null) }
            }
            "privacy" -> {
                val enabled = call.argument<Boolean>("enabled") ?: return result.error("invalid", null, null)
                val covered = call.argument<Boolean>("covered") ?: return result.error("invalid", null, null)
                privacyEnabled = enabled
                if (enabled && activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE == 0) {
                    activity.window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
                    secureAdded = true
                } else if (!enabled && secureAdded) {
                    activity.window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
                    secureAdded = false
                }
                setCover(enabled && covered)
                result.success(null)
            }
            else -> result.notImplemented()
        }
    }

    private fun owner(call: MethodCall): String? = call.argument<String>("owner")?.takeIf { it.matches(Regex("[0-9a-f]{64}")) }
    private fun alias(owner: String) = "luobo.app_access.biometric.v1.$owner"
    private fun keyStore(): KeyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    private fun available(): Boolean {
        if (Build.VERSION.SDK_INT < 28) return false
        val keyguard = activity.getSystemService(KeyguardManager::class.java)
        if (keyguard?.isDeviceSecure != true) return false
        return if (Build.VERSION.SDK_INT >= 30) {
            activity.getSystemService(BiometricManager::class.java)?.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS
        } else {
            // Framework's API28/29 capability query cannot promise strong face
            // authentication. Expose enrolled fingerprint only on these APIs.
            legacyFingerprintAvailable()
        }
    }

    private fun legacyFingerprintAvailable(): Boolean {
        // The public API28/29 class is absent from the compileSdk37 stubs.
        // Resolve only that fixed legacy API at runtime; newer Android uses
        // BIOMETRIC_STRONG above and every actual prompt uses a CryptoObject.
        return try {
            val manager = activity.getSystemService("fingerprint")
            val legacy = Class.forName("android.hardware.fingerprint.FingerprintManager")
            manager != null && legacy.isInstance(manager) &&
                legacy.getMethod("isHardwareDetected").invoke(manager) == true &&
                legacy.getMethod("hasEnrolledFingerprints").invoke(manager) == true
        } catch (_: Exception) { false }
    }

    private fun derive(call: MethodCall, result: MethodChannel.Result) {
        val pin = call.argument<String>("pin")
        val saltText = call.argument<String>("salt")
        val iterations = call.argument<Int>("iterations")
        if (pin == null || !pin.matches(Regex("[0-9]{6,12}")) || saltText == null || iterations == null || iterations !in 210000..1000000) {
            result.error("invalid", null, null); return
        }
        val salt = try { Base64.decode(saltText, Base64.NO_WRAP) } catch (_: Exception) { null }
        if (salt?.size != 32) { result.error("invalid", null, null); return }
        worker.execute {
            val chars = pin.toCharArray()
            try {
                val spec = PBEKeySpec(chars, salt, iterations, 256)
                val bytes = try {
                    SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
                } catch (_: java.security.NoSuchAlgorithmException) {
                    // RFC8018 PBKDF2 using standard JCE HMAC-SHA256, for API24/25.
                    val mac = Mac.getInstance("HmacSHA256")
                    mac.init(SecretKeySpec(pin.toByteArray(Charsets.UTF_8), "HmacSHA256"))
                    var u = mac.doFinal(salt + byteArrayOf(0, 0, 0, 1))
                    val block = u.clone()
                    repeat(iterations - 1) {
                        u = mac.doFinal(u)
                        for (i in block.indices) block[i] = (block[i].toInt() xor u[i].toInt()).toByte()
                    }
                    u.fill(0)
                    block
                } finally { spec.clearPassword() }
                val encoded = Base64.encodeToString(bytes, Base64.NO_WRAP)
                bytes.fill(0)
                activity.runOnUiThread { result.success(encoded) }
            } catch (_: Exception) {
                activity.runOnUiThread { result.error("storage", null, null) }
            } finally { chars.fill('\u0000') }
        }
    }

    private fun authenticate(call: MethodCall, result: MethodChannel.Result, enroll: Boolean) {
        val owner = owner(call) ?: return result.error("invalid", null, null)
        val request = call.argument<Int>("request") ?: return result.error("invalid", null, null)
        if (request < 0 || !available()) { result.error("unavailable", null, null); return }
        if (pending != null) { result.error("cancelled", null, null); return }
        try {
            val store = keyStore()
            if (enroll) {
                remove(owner)
                val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
                val builder = KeyGenParameterSpec.Builder(alias(owner), KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setKeySize(256).setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setUserAuthenticationRequired(true).setInvalidatedByBiometricEnrollment(true)
                if (Build.VERSION.SDK_INT >= 30) builder.setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                else { @Suppress("DEPRECATION") builder.setUserAuthenticationValidityDurationSeconds(-1) }
                generator.init(builder.build()); generator.generateKey()
            }
            val key = store.getKey(alias(owner), null) ?: return result.error("unavailable", null, null)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            if (enroll) cipher.init(Cipher.ENCRYPT_MODE, key)
            else {
                val iv = prefs.getString("$owner.iv", null) ?: return result.error("unavailable", null, null)
                cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)))
            }
            cipher.updateAAD(owner.toByteArray(Charsets.UTF_8))
            val cancelSignal = CancellationSignal()
            cancellation = cancelSignal; pending = result; pendingRequest = request; pendingOwner = owner; pendingEnroll = enroll
            val prompt = BiometricPrompt.Builder(activity).setTitle("验证屏幕锁身份")
                .setSubtitle("使用此设备已登记的生物识别")
                .setNegativeButton("使用解锁密码", activity.mainExecutor) { _, _ -> cancel() }
            if (Build.VERSION.SDK_INT >= 30) prompt.setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
            if (Build.VERSION.SDK_INT >= 29) prompt.setConfirmationRequired(true)
            prompt.build().authenticate(BiometricPrompt.CryptoObject(cipher), cancelSignal, activity.mainExecutor,
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(auth: BiometricPrompt.AuthenticationResult) {
                        if (pending !== result || pendingRequest != request) return
                        try {
                            val actual = auth.cryptoObject?.cipher ?: throw IllegalStateException()
                            if (enroll) {
                                val secret = ByteArray(32).also { SecureRandom().nextBytes(it) }
                                val encrypted = actual.doFinal(secret)
                                secret.fill(0)
                                if (!prefs.edit().putString("$owner.iv", Base64.encodeToString(actual.iv, Base64.NO_WRAP))
                                    .putString("$owner.data", Base64.encodeToString(encrypted, Base64.NO_WRAP)).commit()) throw IllegalStateException()
                            } else {
                                val stored = prefs.getString("$owner.data", null) ?: throw IllegalStateException()
                                val secret = actual.doFinal(Base64.decode(stored, Base64.NO_WRAP))
                                if (secret.size != 32) throw IllegalStateException()
                                secret.fill(0)
                            }
                            finish(result, request, null)
                        } catch (_: Exception) { finish(result, request, "unavailable") }
                    }
                    override fun onAuthenticationError(code: Int, text: CharSequence) {
                        val error = when (code) {
                            BiometricPrompt.BIOMETRIC_ERROR_LOCKOUT, BiometricPrompt.BIOMETRIC_ERROR_LOCKOUT_PERMANENT -> "locked_out"
                            BiometricPrompt.BIOMETRIC_ERROR_CANCELED, BiometricPrompt.BIOMETRIC_ERROR_USER_CANCELED -> "cancelled"
                            else -> "unavailable"
                        }
                        finish(result, request, error)
                    }
                })
        } catch (_: Exception) {
            if (pending === result) finish(result, request, "unavailable")
            else result.error("unavailable", null, null)
        }
    }

    private fun finish(result: MethodChannel.Result, request: Int, error: String?) {
        if (pending !== result || pendingRequest != request) return
        val owner = pendingOwner
        val removeFailedEnrollment = pendingEnroll && error != null
        pending = null; pendingRequest = null; pendingOwner = null; pendingEnroll = false; cancellation = null
        if (removeFailedEnrollment && owner != null) try { remove(owner) } catch (_: Exception) {}
        if (error == null) result.success(request) else result.error(error, null, null)
    }

    fun cancel() {
        val result = pending; val request = pendingRequest
        val signal = cancellation
        if (result != null && request != null) finish(result, request, "cancelled")
        signal?.cancel()
    }
    private fun remove(owner: String) {
        keyStore().deleteEntry(alias(owner))
        if (!prefs.edit().remove("$owner.iv").remove("$owner.data").commit()) throw IllegalStateException()
    }
    fun inactive() { if (privacyEnabled) setCover(true) }
    fun stopped() { cancel() }
    private fun setCover(show: Boolean) {
        val root = activity.window.decorView as? FrameLayout ?: return
        if (show && cover == null) {
            cover = View(activity).apply { setBackgroundColor(0xfff4f5f7.toInt()); isClickable = true; importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS }
            root.addView(cover, FrameLayout.LayoutParams(-1, -1))
        } else if (!show) { cover?.let { root.removeView(it) }; cover = null }
    }
    fun dispose() { cancel(); channel.setMethodCallHandler(null); worker.shutdown() }
}

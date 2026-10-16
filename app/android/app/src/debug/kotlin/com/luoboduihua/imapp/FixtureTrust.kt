package com.luoboduihua.imapp

import android.content.Context
import android.system.Os
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

/** Task-only CA, packaged only with -PluoboFixtureCa=/absolute/public/ca.pem. */
object FixtureTrust {
    @JvmStatic
    fun configure(context: Context) {
        val resource = context.resources.getIdentifier("luobo_fixture_ca", "raw", context.packageName)
        if (resource == 0) return
        val expectedResource = context.resources.getIdentifier("luobo_fixture_ca_sha256", "string", context.packageName)
        check(expectedResource != 0) { "Incomplete explicit fixture trust configuration" }
        val bytes = context.resources.openRawResource(resource).use { it.readBytes() }
        val digest = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        check(digest == context.getString(expectedResource)) { "Fixture CA fingerprint mismatch" }
        val certificate = CertificateFactory.getInstance("X.509").generateCertificate(bytes.inputStream()) as X509Certificate
        certificate.checkValidity()
        check(certificate.basicConstraints >= 0) { "Fixture trust requires a CA certificate" }
        certificate.verify(certificate.publicKey)
        val file = java.io.File(context.filesDir, "fixture-ca.pem")
        file.writeBytes(bytes)
        file.setReadable(false, false)
        file.setReadable(true, true)
        file.setWritable(false, false)
        file.setWritable(true, true)
        Os.setenv("SSL_CERT_FILE", file.absolutePath, true)
        Os.setenv("HTTPS_PROXY", "http://10.0.2.2:38081", true)
        Os.setenv("HTTP_PROXY", "http://10.0.2.2:38081", true)
        Os.setenv("NO_PROXY", "localhost,127.0.0.1,::1", true)
        System.setProperty("https.proxyHost", "10.0.2.2")
        System.setProperty("https.proxyPort", "38081")
        System.setProperty("http.proxyHost", "10.0.2.2")
        System.setProperty("http.proxyPort", "38081")
    }
}

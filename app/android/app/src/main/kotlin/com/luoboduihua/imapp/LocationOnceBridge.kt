package com.luoboduihua.imapp

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.os.Build
import android.os.Bundle
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

/** Foreground, one-shot OS location only. This channel has no WebView or SDK sender. */
class LocationOnceBridge(private val activity: Activity, messenger: BinaryMessenger) {
    private enum class Phase { PERMISSION, RESUME, FIX }
    private class Request(val id: String, val result: MethodChannel.Result, val deadline: Long) {
        var phase = Phase.PERMISSION
        var permissionCode: Int? = null
        var timer: Runnable? = null
        var signal: CancellationSignal? = null
        var listener: LocationListener? = null
    }

    private val channel = MethodChannel(messenger, "luobo/location_once")
    private val handler = Handler(Looper.getMainLooper())
    private var manager: LocationManager? = null
    private var pending: Request? = null
    private var resumed = false
    private var disposed = false
    // Never reuse a permission callback code during this engine's lifetime.
    private var nextPermissionCode = 0x6c00

    init {
        channel.setMethodCallHandler { call, result ->
            if (Looper.myLooper() == Looper.getMainLooper()) handle(call, result)
            else handler.post { handle(call, result) }
        }
    }

    private fun id(args: Map<*, *>, keys: Set<String>): String? {
        if (args.keys != keys) return null
        val value = args["requestId"] as? String ?: return null
        return value.takeIf { Regex("[0-9a-f]{32}").matches(it) }
    }

    private fun handle(call: MethodCall, result: MethodChannel.Result) {
        val args = call.arguments as? Map<*, *>
        if (call.method == "cancel") {
            val value = args?.let { id(it, setOf("requestId")) }
            if (value == null) { result.error("invalid_request", null, null); return }
            val request = pending
            val matched = request != null && request.id == value
            if (matched) finish(request!!, "cancelled")
            result.success(mapOf("requestId" to value, "cancelled" to matched))
            return
        }
        if (call.method != "request") { result.notImplemented(); return }
        val value = args?.let { id(it, setOf("requestId", "timeoutMs")) }
        val timeoutValue = args?.get("timeoutMs")
        val timeout = when (timeoutValue) {
            is Int -> timeoutValue.toLong()
            is Long -> timeoutValue
            else -> null
        }
        if (value == null || timeout == null || timeout !in 1000L..20000L) {
            result.error("invalid_request", null, null); return
        }
        if (pending != null) { result.error("busy", null, null); return }
        if (disposed) { result.error("unavailable", null, null); return }
        if (!foreground()) { result.error("cancelled", null, null); return }
        if (manager == null) manager = activity.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
        if (manager == null) { result.error("unavailable", null, null); return }
        val request = Request(value, result, SystemClock.elapsedRealtime() + timeout)
        pending = request
        request.timer = Runnable { finish(request, "timeout") }
        handler.postDelayed(request.timer!!, timeout)
        if (!serviceEnabled()) { finish(request, "service_off"); return }
        if (permission(Manifest.permission.ACCESS_COARSE_LOCATION) || permission(Manifest.permission.ACCESS_FINE_LOCATION)) {
            startFix(request)
        } else {
            if (nextPermissionCode > 0x7bff) { finish(request, "unavailable"); return }
            val code = nextPermissionCode++
            request.permissionCode = code
            try {
                activity.requestPermissions(arrayOf(Manifest.permission.ACCESS_COARSE_LOCATION, Manifest.permission.ACCESS_FINE_LOCATION), code)
            } catch (_: RuntimeException) { finish(request, "unavailable") }
        }
    }

    fun permissionResult(requestCode: Int) {
        val request = pending ?: return
        if (request.permissionCode != requestCode || request.phase != Phase.PERMISSION) return
        request.permissionCode = null
        if (!permission(Manifest.permission.ACCESS_COARSE_LOCATION) && !permission(Manifest.permission.ACCESS_FINE_LOCATION)) {
            val canAsk = activity.shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_COARSE_LOCATION) ||
                activity.shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_FINE_LOCATION)
            finish(request, if (canAsk) "denied" else "permanently_denied")
            return
        }
        request.phase = Phase.RESUME
        if (foreground()) startFix(request)
    }

    fun resumed() {
        resumed = true
        pending?.takeIf { it.phase == Phase.RESUME }?.let { startFix(it) }
    }

    fun paused() {
        resumed = false
        // Only our unresolved permission prompt may survive inactivity; no active fix does.
        pending?.takeIf { it.phase != Phase.PERMISSION }?.let { finish(it, "cancelled") }
    }

    fun stopped() {
        resumed = false
        pending?.let { finish(it, "cancelled") }
    }

    private fun foreground() = resumed && !activity.isFinishing && !activity.isDestroyed
    private fun permission(name: String) = activity.checkSelfPermission(name) == PackageManager.PERMISSION_GRANTED
    private fun serviceEnabled(): Boolean = try {
        if (Build.VERSION.SDK_INT >= 28) manager?.isLocationEnabled == true
        else manager?.isProviderEnabled(LocationManager.GPS_PROVIDER) == true || manager?.isProviderEnabled(LocationManager.NETWORK_PROVIDER) == true
    } catch (_: RuntimeException) { false }

    @Suppress("DEPRECATION")
    private fun startFix(request: Request) {
        if (pending !== request) return
        if (!foreground()) { finish(request, "cancelled"); return }
        if (SystemClock.elapsedRealtime() >= request.deadline) { finish(request, "timeout"); return }
        if (!serviceEnabled()) { finish(request, "service_off"); return }
        val fine = permission(Manifest.permission.ACCESS_FINE_LOCATION)
        if (!fine && !permission(Manifest.permission.ACCESS_COARSE_LOCATION)) { finish(request, "denied"); return }
        val service = manager ?: run { finish(request, "unavailable"); return }
        try {
            val provider = when {
                fine && service.isProviderEnabled(LocationManager.GPS_PROVIDER) -> LocationManager.GPS_PROVIDER
                service.isProviderEnabled(LocationManager.NETWORK_PROVIDER) -> LocationManager.NETWORK_PROVIDER
                else -> { finish(request, "unavailable"); return }
            }
            request.phase = Phase.FIX
            if (Build.VERSION.SDK_INT >= 30) {
                val signal = CancellationSignal(); request.signal = signal
                service.getCurrentLocation(provider, signal, activity.mainExecutor) { location -> accept(request, location) }
            } else {
                val listener = object : LocationListener {
                    override fun onLocationChanged(location: Location) { accept(request, location) }
                    override fun onProviderDisabled(provider: String) { if (pending === request) finish(request, "service_off") }
                    override fun onProviderEnabled(provider: String) {}
                    override fun onStatusChanged(provider: String?, status: Int, extras: Bundle?) {}
                }
                request.listener = listener
                service.requestSingleUpdate(provider, listener, Looper.getMainLooper())
            }
        } catch (_: SecurityException) { finish(request, "denied") }
        catch (_: RuntimeException) { finish(request, "unavailable") }
    }

    private fun accept(request: Request, location: Location?) {
        if (pending !== request) return
        if (!foreground()) { finish(request, "cancelled"); return }
        if (SystemClock.elapsedRealtime() >= request.deadline) { finish(request, "timeout"); return }
        if (!serviceEnabled()) { finish(request, "service_off"); return }
        if (location == null) { finish(request, "unavailable"); return }
        val ageNanos = SystemClock.elapsedRealtimeNanos() - location.elapsedRealtimeNanos
        val wallAge = System.currentTimeMillis() - location.time
        if (!location.latitude.isFinite() || location.latitude !in -90.0..90.0 ||
            !location.longitude.isFinite() || location.longitude !in -180.0..180.0 ||
            !location.hasAccuracy() || !location.accuracy.isFinite() || location.accuracy < 0 ||
            location.elapsedRealtimeNanos <= 0 || ageNanos !in 0L..10000000000L || location.time <= 0 || wallAge !in -2000L..10000L) {
            finish(request, "unavailable"); return
        }
        if (!permission(Manifest.permission.ACCESS_COARSE_LOCATION) && !permission(Manifest.permission.ACCESS_FINE_LOCATION)) {
            finish(request, "denied"); return
        }
        finish(request, value = mapOf("requestId" to request.id, "latitude" to location.latitude,
            "longitude" to location.longitude, "accuracyMeters" to location.accuracy.toDouble(),
            "timestampMillis" to location.time, "coordinateSystem" to "wgs84",
            "approximate" to !permission(Manifest.permission.ACCESS_FINE_LOCATION)))
    }

    private fun finish(request: Request, error: String? = null, value: Map<String, Any>? = null) {
        if (pending !== request) return
        pending = null // Retire before OS cleanup or Flutter callbacks can reenter.
        request.timer?.let { handler.removeCallbacks(it) }; request.timer = null
        try { request.signal?.cancel() } catch (_: RuntimeException) {}
        request.signal = null
        request.listener?.let { try { manager?.removeUpdates(it) } catch (_: RuntimeException) {} }
        request.listener = null
        if (error != null) request.result.error(error, null, null)
        else if (value != null) request.result.success(value)
        else request.result.error("unavailable", null, null)
    }

    fun dispose() {
        disposed = true
        stopped()
        manager = null
        channel.setMethodCallHandler(null)
    }
}

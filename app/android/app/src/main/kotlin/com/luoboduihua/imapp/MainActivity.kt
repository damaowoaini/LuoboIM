package com.luoboduihua.imapp

import io.flutter.embedding.android.FlutterActivity
import android.content.pm.ApplicationInfo
import android.os.Bundle
import io.flutter.embedding.engine.FlutterEngine

class MainActivity : FlutterActivity() {
    private var appAccess: AppAccessBridge? = null
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        appAccess = AppAccessBridge(this, flutterEngine.dartExecutor.binaryMessenger)
    }
    override fun onPause() { appAccess?.inactive(); super.onPause() }
    override fun onStop() { appAccess?.stopped(); super.onStop() }
    override fun cleanUpFlutterEngine(flutterEngine: FlutterEngine) {
        appAccess?.dispose(); appAccess = null
        super.cleanUpFlutterEngine(flutterEngine)
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        // The optional helper and its CA resources exist only in an explicitly
        // configured debug build. Configure before Go/Flutter plugins load.
        if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0) {
            try {
                Class.forName("com.luoboduihua.imapp.FixtureTrust")
                    .getMethod("configure", android.content.Context::class.java)
                    .invoke(null, this)
            } catch (_: ClassNotFoundException) {
                // Production/profile source sets contain no fixture helper.
            }
        }
        super.onCreate(savedInstanceState)
    }
}

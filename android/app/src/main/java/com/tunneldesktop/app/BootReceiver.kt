package com.tunneldesktop.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_LOCKED_BOOT_COMPLETED) {
            val storage = if (Build.VERSION.SDK_INT >= 24) context.createDeviceProtectedStorageContext() else context
            val prefs = storage.getSharedPreferences("relay", Context.MODE_PRIVATE)
            if (prefs.getBoolean("autostart", false)) {
                context.startForegroundService(Intent(context, RelayService::class.java))
                WatchdogReceiver.schedule(context)
            }
        }
    }
}

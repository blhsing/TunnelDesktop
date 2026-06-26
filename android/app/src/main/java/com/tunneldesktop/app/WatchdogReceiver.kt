package com.tunneldesktop.app

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

class WatchdogReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val storage = if (Build.VERSION.SDK_INT >= 24) context.createDeviceProtectedStorageContext() else context
        val prefs = storage.getSharedPreferences("relay", Context.MODE_PRIVATE)
        if (prefs.getBoolean("running", false)) {
            context.startForegroundService(Intent(context, RelayService::class.java))
        }
        schedule(context)
    }

    companion object {
        fun schedule(context: Context) {
            val alarm = context.getSystemService(Context.ALARM_SERVICE) as AlarmManager
            val intent = PendingIntent.getBroadcast(
                context,
                1002,
                Intent(context, WatchdogReceiver::class.java),
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )
            val at = System.currentTimeMillis() + 60_000
            if (Build.VERSION.SDK_INT >= 23) {
                alarm.setExactAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, at, intent)
            } else {
                alarm.setExact(AlarmManager.RTC_WAKEUP, at, intent)
            }
        }
    }
}

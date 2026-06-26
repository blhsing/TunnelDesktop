package com.tunneldesktop.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import androidx.core.app.NotificationCompat
import relaycore.Relaycore

class RelayService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onCreate() {
        super.onCreate()
        createChannel()
        startForeground(1001, notification("Running"))
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        if (wakeLock == null) {
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "TunnelDesktop:Relay").apply {
                setReferenceCounted(false)
                acquire()
            }
        }
        val wifi = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
        if (wifiLock == null) {
            wifiLock = wifi.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "TunnelDesktop:Wifi").apply {
                setReferenceCounted(false)
                acquire()
            }
        }
        val config = prefs().getString("config", "{}") ?: "{}"
        Relaycore.configure(config)
        Relaycore.start()
        WatchdogReceiver.schedule(this)
        return START_STICKY
    }

    override fun onDestroy() {
        Relaycore.stop()
        wakeLock?.release()
        wakeLock = null
        wifiLock?.release()
        wifiLock = null
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun createChannel() {
        if (Build.VERSION.SDK_INT >= 26) {
            val channel = NotificationChannel("relay", "TunnelDesktop", NotificationManager.IMPORTANCE_LOW)
            getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }
    }

    private fun notification(text: String): Notification =
        NotificationCompat.Builder(this, "relay")
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle("TunnelDesktop")
            .setContentText(text)
            .setOngoing(true)
            .build()

    private fun prefs() =
        if (Build.VERSION.SDK_INT >= 24) createDeviceProtectedStorageContext().getSharedPreferences("relay", MODE_PRIVATE)
        else getSharedPreferences("relay", MODE_PRIVATE)
}

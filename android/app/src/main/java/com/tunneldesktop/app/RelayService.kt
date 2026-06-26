package com.tunneldesktop.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.SharedPreferences
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
        return try {
            if (Relaycore.status() == "running") {
                prefs().edit()
                    .putBoolean("running", true)
                    .remove("last_error")
                    .apply()
                updateNotification("Running")
                WatchdogReceiver.schedule(this)
                return START_STICKY
            }
            val config = prefs().getString("config", "") ?: ""
            if (config.isBlank()) {
                throw IllegalStateException("Generate bundles before starting the relay")
            }
            AndroidRelayPorts.requireConfigListenPort(config)
            acquireLocks()
            Relaycore.configure(config)
            Relaycore.start()
            prefs().edit()
                .putBoolean("running", true)
                .remove("last_error")
                .apply()
            updateNotification("Running")
            WatchdogReceiver.schedule(this)
            START_STICKY
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            if (Relaycore.status() == "running" || message == "relay is running") {
                prefs().edit()
                    .putBoolean("running", true)
                    .remove("last_error")
                    .apply()
                updateNotification("Running")
                WatchdogReceiver.schedule(this)
                return START_STICKY
            }
            prefs().edit()
                .putBoolean("running", false)
                .putString("last_error", message)
                .apply()
            updateNotification("Start failed")
            releaseLocks()
            stopSelf(startId)
            START_NOT_STICKY
        }
    }

    override fun onDestroy() {
        try {
            Relaycore.stop()
        } catch (_: Exception) {
        }
        releaseLocks()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun acquireLocks() {
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
    }

    private fun releaseLocks() {
        wakeLock?.release()
        wakeLock = null
        wifiLock?.release()
        wifiLock = null
    }

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

    private fun updateNotification(text: String) {
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(1001, notification(text))
    }

    private fun prefs(): SharedPreferences =
        if (Build.VERSION.SDK_INT >= 24) createDeviceProtectedStorageContext().getSharedPreferences("relay", MODE_PRIVATE)
        else getSharedPreferences("relay", MODE_PRIVATE)
}

package com.tunneldesktop.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import androidx.core.app.NotificationCompat
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

class RelayService : Service() {
    private val handler = Handler(Looper.getMainLooper())
    private var client: OkHttpClient? = null
    private var webSocket: WebSocket? = null
    private var stopping = false

    override fun onCreate() {
        super.onCreate()
        createChannel()
        startForeground(1001, notification("Connecting"))
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        stopping = false
        connect()
        WatchdogReceiver.schedule(this)
        return START_STICKY
    }

    override fun onDestroy() {
        stopping = true
        webSocket?.close(1000, "stopped")
        webSocket = null
        client?.dispatcher?.executorService?.shutdown()
        client = null
        prefs().edit()
            .putBoolean("running", false)
            .putString("home_agent_status", "stopped")
            .apply()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun connect() {
        try {
            val relayUrl = prefs().getString("relay_addr", "") ?: ""
            if (relayUrl.isBlank()) {
                throw IllegalStateException("Enter the relay URL before starting the home agent")
            }
            prefs().edit()
                .putBoolean("running", true)
                .putString("home_agent_status", "connecting")
                .remove("last_error")
                .apply()
            updateNotification("Connecting")

            val httpClient = OkHttpClient.Builder()
                .pingInterval(20, TimeUnit.SECONDS)
                .retryOnConnectionFailure(true)
                .build()
            client = httpClient
            val request = Request.Builder()
                .url(webSocketUrl(relayUrl))
                .header("X-TunnelDesktop-Role", "home-agent")
                .header("X-TunnelDesktop-Hotspot-IP", PhoneNetwork.hotspotIp().orEmpty())
                .header("X-TunnelDesktop-Private-IPs", PhoneNetwork.privateIpSummary())
                .build()
            webSocket = httpClient.newWebSocket(request, object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    prefs().edit()
                        .putBoolean("running", true)
                        .putString("home_agent_status", "connected")
                        .putString("home_agent_remote", relayUrl)
                        .remove("last_error")
                        .apply()
                    updateNotification("Connected")
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    markDisconnected("closed: $code $reason")
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    val status = response?.code?.let { "HTTP $it: " }.orEmpty()
                    markDisconnected(status + (t.message ?: t.javaClass.simpleName))
                }
            })
        } catch (e: Exception) {
            failStart(e.message ?: e.javaClass.simpleName)
        }
    }

    private fun markDisconnected(message: String) {
        if (stopping) return
        prefs().edit()
            .putString("home_agent_status", "reconnecting")
            .putString("last_error", message)
            .apply()
        updateNotification("Reconnecting")
        handler.postDelayed({ if (!stopping) connect() }, 5000)
    }

    private fun failStart(message: String) {
        prefs().edit()
            .putBoolean("running", false)
            .putString("home_agent_status", "failed")
            .putString("last_error", message)
            .apply()
        updateNotification("Start failed")
        stopSelf()
    }

    private fun webSocketUrl(relayUrl: String): String {
        val uri = Uri.parse(relayUrl.trim())
        val scheme = when (uri.scheme?.lowercase()) {
            "https" -> "wss"
            "http" -> "ws"
            "wss", "ws" -> uri.scheme!!.lowercase()
            else -> throw IllegalArgumentException("Relay URL must start with https://")
        }
        val basePath = uri.path.orEmpty().trimEnd('/')
        val wsPath = if (basePath.endsWith("/ws")) basePath else "$basePath/ws"
        return uri.buildUpon()
            .scheme(scheme)
            .encodedPath(wsPath.ifBlank { "/relay/ws" })
            .clearQuery()
            .build()
            .toString()
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

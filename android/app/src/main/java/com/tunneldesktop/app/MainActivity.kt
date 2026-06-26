package com.tunneldesktop.app

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat

class MainActivity : AppCompatActivity() {
    private companion object {
        const val DEFAULT_RELAY_URL = "https://test-officialwebsite.azurewebsites.net/relay/workdesk"
    }

    private lateinit var prefs: SharedPreferences
    private lateinit var relayUrl: EditText
    private lateinit var status: TextView
    private lateinit var details: TextView
    private lateinit var commands: TextView
    private lateinit var phoneNetwork: TextView
    private lateinit var toggle: Button

    private val statusHandler = Handler(Looper.getMainLooper())
    private val statusRefresh = object : Runnable {
        override fun run() {
            refreshStatus()
            statusHandler.postDelayed(this, 2000)
        }
    }
    private val preferenceListener = SharedPreferences.OnSharedPreferenceChangeListener { _, _ ->
        runOnUiThread { refreshStatus() }
    }

    private val ink = Color.rgb(31, 41, 55)
    private val muted = Color.rgb(93, 104, 116)
    private val page = Color.rgb(246, 248, 247)
    private val panel = Color.WHITE
    private val line = Color.rgb(211, 219, 224)
    private val accent = Color.rgb(47, 111, 115)
    private val accentSoft = Color.rgb(227, 244, 242)
    private val success = Color.rgb(43, 125, 82)
    private val danger = Color.rgb(172, 67, 67)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = deviceProtectedStorageContext().getSharedPreferences("relay", MODE_PRIVATE)
        requestNotifications()
        buildUi()
        refreshStatus()
    }

    override fun onResume() {
        super.onResume()
        prefs.registerOnSharedPreferenceChangeListener(preferenceListener)
        statusHandler.removeCallbacks(statusRefresh)
        statusHandler.post(statusRefresh)
    }

    override fun onPause() {
        statusHandler.removeCallbacks(statusRefresh)
        prefs.unregisterOnSharedPreferenceChangeListener(preferenceListener)
        super.onPause()
    }

    private fun buildUi() {
        val root = ScrollView(this).apply {
            setBackgroundColor(page)
            isFillViewport = true
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(20), dp(20), dp(20))
        }
        root.addView(content, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        relayUrl = edit(savedRelayUrl())
        status = statusLabel()
        details = infoBox()
        commands = infoBox()
        phoneNetwork = infoBox()
        toggle = button("Start", primary = true) { toggleHomeAgent() }

        val autostart = CheckBox(this).apply {
            text = "Start on boot"
            isChecked = prefs.getBoolean("autostart", true)
            setTextColor(ink)
            textSize = 14f
            setOnCheckedChangeListener { _, checked ->
                prefs.edit().putBoolean("autostart", checked).apply()
            }
        }

        content.addView(title("TunnelDesktop Home Agent"))
        addSpaced(content, status, 10)
        addSpaced(
            content,
            section(
                "Relay Room",
                field("Shared relay URL", relayUrl),
                actionRow(
                    button("Use default") { applyDefaultUrl() },
                    button("Copy URL") { copyText("TunnelDesktop relay URL", normalizedRelayUrl()) }
                ),
                commands
            )
        )
        addSpaced(
            content,
            section(
                "Phone Network",
                phoneNetwork,
                actionRow(
                    button("Copy hotspot IP") { copyHotspotIp() },
                    button("Refresh") { refreshStatus("Network refreshed") }
                )
            )
        )
        addSpaced(
            content,
            section(
                "Connection",
                toggle,
                details,
                autostart,
                actionRow(
                    button("Copy agent") { copyText("TunnelDesktop work agent command", "agent.exe -relay-url ${normalizedRelayUrl()}") },
                    button("Copy client") { copyText("TunnelDesktop home client command", "client.exe -relay-url ${normalizedRelayUrl()}") }
                ),
                actionRow(
                    button("Battery") { requestBatteryExemption() },
                    button("App settings") { openAppBatterySettings() }
                ),
                button("VPN persistence") { requestVpnPersistence() }
            )
        )
        setContentView(root)
    }

    private fun toggleHomeAgent() {
        val running = prefs.getBoolean("running", false)
        val lastError = prefs.getString("last_error", "") ?: ""
        if (running && lastError.isBlank()) stopHomeAgent() else startHomeAgent()
    }

    private fun startHomeAgent() {
        try {
            val url = normalizedRelayUrl()
            prefs.edit()
                .putString("relay_addr", url)
                .putBoolean("running", true)
                .putString("home_agent_status", "connecting")
                .remove("last_error")
                .apply()
            val intent = Intent(this, RelayService::class.java)
            if (Build.VERSION.SDK_INT >= 26) startForegroundService(intent) else startService(intent)
            refreshStatus("Starting")
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            prefs.edit()
                .putBoolean("running", false)
                .putString("home_agent_status", "failed")
                .putString("last_error", message)
                .apply()
            refreshStatus("Start failed: $message")
        }
    }

    private fun stopHomeAgent() {
        stopService(Intent(this, RelayService::class.java))
        prefs.edit()
            .putBoolean("running", false)
            .putString("home_agent_status", "stopped")
            .remove("last_error")
            .apply()
        refreshStatus("Stopped")
    }

    private fun refreshStatus(extra: String = "") {
        val url = normalizedRelayUrl()
        val running = prefs.getBoolean("running", false)
        val state = prefs.getString("home_agent_status", "stopped") ?: "stopped"
        val lastError = prefs.getString("last_error", "") ?: ""
        val model = when {
            state == "connected" -> StatusModel("Home agent connected", success, Color.rgb(230, 246, 237))
            running && lastError.isBlank() -> StatusModel("Home agent connecting", accent, accentSoft)
            lastError.isNotBlank() -> StatusModel("Home agent failed", danger, Color.rgb(252, 232, 232))
            else -> StatusModel("Home agent stopped", muted, Color.rgb(239, 242, 245))
        }
        status.text = model.text
        status.setTextColor(model.textColor)
        status.background = rounded(model.backgroundColor, Color.TRANSPARENT)
        toggle.text = if (running && lastError.isBlank()) "Stop" else "Start"
        styleButton(toggle, primary = !running || lastError.isNotBlank(), danger = running && lastError.isBlank())

        details.text = listOf(
            "Relay URL: $url",
            "State: $state",
            if (lastError.isNotBlank()) "Last error: $lastError" else "",
            extra
        ).filter { it.isNotBlank() }.joinToString("\n")
        commands.text = listOf(
            "Work agent: agent.exe -relay-url $url",
            "Home client: client.exe -relay-url $url"
        ).joinToString("\n")
        phoneNetwork.text = phoneNetworkText()
    }

    private fun savedRelayUrl(): String =
        (prefs.getString("relay_addr", "") ?: "").ifBlank { DEFAULT_RELAY_URL }

    private fun normalizedRelayUrl(): String {
        val value = relayUrl.text.toString().trim().ifBlank { DEFAULT_RELAY_URL }
        val parsed = Uri.parse(value)
        require(parsed.scheme == "https" || parsed.scheme == "http" || parsed.scheme == "wss" || parsed.scheme == "ws") {
            "Relay URL must start with https://"
        }
        require(!parsed.host.isNullOrBlank()) { "Relay URL must include a host" }
        return value.trimEnd('/')
    }

    private fun applyDefaultUrl() {
        relayUrl.setText(DEFAULT_RELAY_URL)
        prefs.edit().putString("relay_addr", DEFAULT_RELAY_URL).apply()
        refreshStatus("Default URL applied")
    }

    private fun copyText(label: String, value: String) {
        prefs.edit().putString("relay_addr", normalizedRelayUrl()).apply()
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText(label, value))
        Toast.makeText(this, "Copied", Toast.LENGTH_SHORT).show()
    }

    private fun copyHotspotIp() {
        val candidate = PhoneNetwork.privateIpv4Candidates().firstOrNull()
        if (candidate == null) {
            Toast.makeText(this, "No hotspot IP detected", Toast.LENGTH_SHORT).show()
            return
        }
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("TunnelDesktop hotspot IP", candidate.address))
        Toast.makeText(this, "Copied ${candidate.address}", Toast.LENGTH_SHORT).show()
    }

    private fun phoneNetworkText(): String {
        val candidates = PhoneNetwork.privateIpv4Candidates()
        val primary = candidates.firstOrNull()
        if (primary == null) {
            return "Hotspot IP: not detected\nPrivate IPv4: none"
        }
        val others = candidates.drop(1)
        return listOf(
            "Hotspot IP: ${primary.address}",
            "Interface: ${primary.interfaceName}",
            if (others.isNotEmpty()) {
                "Other private IPv4: " + others.joinToString(", ") { "${it.address} (${it.interfaceName})" }
            } else {
                ""
            }
        ).filter { it.isNotBlank() }.joinToString("\n")
    }

    private fun title(text: String): TextView =
        TextView(this).apply {
            this.text = text
            textSize = 26f
            typeface = Typeface.DEFAULT_BOLD
            setTextColor(ink)
        }

    private fun section(titleText: String, vararg children: View): LinearLayout =
        LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(14), dp(12), dp(14), dp(14))
            background = rounded(panel, line)
            elevation = dp(1).toFloat()
            addView(TextView(this@MainActivity).apply {
                text = titleText
                textSize = 15f
                typeface = Typeface.DEFAULT_BOLD
                setTextColor(ink)
            })
            children.forEach { addSpaced(this, it, 10) }
        }

    private fun field(labelText: String, input: EditText): LinearLayout =
        LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(TextView(this@MainActivity).apply {
                text = labelText
                textSize = 12f
                typeface = Typeface.DEFAULT_BOLD
                setTextColor(muted)
            })
            addSpaced(this, input, 4)
        }

    private fun edit(value: String): EditText =
        EditText(this).apply {
            hint = DEFAULT_RELAY_URL
            setText(value)
            setSingleLine(true)
            textSize = 14f
            setTextColor(ink)
            setHintTextColor(Color.rgb(130, 140, 150))
            setPadding(dp(12), 0, dp(12), 0)
            minHeight = dp(46)
            background = rounded(Color.WHITE, line)
        }

    private fun statusLabel(): TextView =
        TextView(this).apply {
            textSize = 13f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(dp(12), dp(8), dp(12), dp(8))
        }

    private fun infoBox(): TextView =
        TextView(this).apply {
            textSize = 13f
            setTextColor(ink)
            setPadding(dp(12), dp(10), dp(12), dp(10))
            background = rounded(Color.rgb(245, 247, 250), Color.TRANSPARENT)
            setTextIsSelectable(true)
        }

    private fun actionRow(vararg buttons: Button): LinearLayout =
        LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            buttons.forEachIndexed { index, button ->
                val params = LinearLayout.LayoutParams(0, dp(46), 1f).apply {
                    if (index > 0) marginStart = dp(8)
                }
                addView(button, params)
            }
        }

    private fun button(text: String, primary: Boolean = false, danger: Boolean = false, action: () -> Unit): Button =
        Button(this).apply {
            this.text = text
            isAllCaps = false
            textSize = 14f
            typeface = Typeface.DEFAULT_BOLD
            styleButton(this, primary, danger)
            setPadding(dp(8), 0, dp(8), 0)
            minHeight = dp(44)
            setOnClickListener { action() }
        }

    private fun styleButton(button: Button, primary: Boolean = false, danger: Boolean = false) {
        button.setTextColor(if (primary || danger) Color.WHITE else accent)
        button.background = when {
            danger -> rounded(this@MainActivity.danger, Color.TRANSPARENT)
            primary -> rounded(accent, Color.TRANSPARENT)
            else -> rounded(Color.WHITE, accent)
        }
    }

    private fun addSpaced(parent: LinearLayout, child: View, top: Int = 12) {
        val requestedHeight = child.layoutParams?.height ?: ViewGroup.LayoutParams.WRAP_CONTENT
        val params = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, requestedHeight).apply {
            topMargin = dp(top)
        }
        parent.addView(child, params)
    }

    private fun rounded(fill: Int, stroke: Int, radius: Int = 8): GradientDrawable =
        GradientDrawable().apply {
            setColor(fill)
            cornerRadius = dp(radius).toFloat()
            if (stroke != Color.TRANSPARENT) setStroke(dp(1), stroke)
        }

    private fun dp(value: Int): Int =
        (value * resources.displayMetrics.density + 0.5f).toInt()

    private fun requestNotifications() {
        if (Build.VERSION.SDK_INT >= 33) {
            ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }

    private fun requestBatteryExemption() {
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        if (!pm.isIgnoringBatteryOptimizations(packageName)) {
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                data = Uri.parse("package:$packageName")
            })
        }
    }

    private fun openAppBatterySettings() {
        startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS).apply {
            data = Uri.parse("package:$packageName")
        })
    }

    private fun requestVpnPersistence() {
        val prepare = VpnService.prepare(this)
        if (prepare != null) startActivityForResult(prepare, 42) else startService(Intent(this, PersistenceVpnService::class.java))
    }

    private fun deviceProtectedStorageContext() =
        if (Build.VERSION.SDK_INT >= 24) createDeviceProtectedStorageContext() else this

    private data class StatusModel(val text: String, val textColor: Int, val backgroundColor: Int)
}

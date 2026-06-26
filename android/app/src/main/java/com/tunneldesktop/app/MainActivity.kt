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
import org.json.JSONArray
import org.json.JSONObject
import relaycore.Relaycore
import java.net.Inet6Address
import java.net.NetworkInterface
import java.util.Collections

class MainActivity : AppCompatActivity() {
    private lateinit var prefs: SharedPreferences
    private lateinit var relayAddr: EditText
    private lateinit var relayHosts: EditText
    private lateinit var agentProxy: EditText
    private lateinit var rawAllow: EditText
    private lateinit var publicIpv6: TextView
    private lateinit var status: TextView
    private lateinit var bundleState: TextView
    private lateinit var logs: TextView
    private lateinit var relayToggle: Button
    private val statusHandler = Handler(Looper.getMainLooper())
    private val statusRefresh = object : Runnable {
        override fun run() {
            refreshStatus()
            statusHandler.postDelayed(this, 2000)
        }
    }
    private val preferenceListener = SharedPreferences.OnSharedPreferenceChangeListener { _, key ->
        if (key == "running" || key == "last_error" || key == "config") {
            runOnUiThread { refreshStatus() }
        }
    }

    private val ink = Color.rgb(31, 41, 55)
    private val muted = Color.rgb(93, 104, 116)
    private val page = Color.rgb(246, 248, 247)
    private val panel = Color.rgb(255, 255, 255)
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
        Relaycore.setLogCallback { line ->
            runOnUiThread {
                logs.append(line + "\n")
            }
        }
        refreshStatus()
    }

    override fun onResume() {
        super.onResume()
        prefs.registerOnSharedPreferenceChangeListener(preferenceListener)
        refreshStatus()
        statusHandler.removeCallbacks(statusRefresh)
        statusHandler.postDelayed(statusRefresh, 2000)
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

        relayAddr = edit("phone.example.com:443 or [ipv6]:443", prefs.getString("relay_addr", "phone.example.com:443") ?: "")
        relayHosts = edit("phone.example.com, 2001:db8::42", prefs.getString("relay_hosts", "phone.example.com") ?: "")
        agentProxy = edit("Blank for direct, or http://proxy.example:8080", savedAgentProxy())
        rawAllow = edit("192.168.43.0/24", prefs.getString("raw_allow", "192.168.43.0/24") ?: "")
        publicIpv6 = TextView(this).apply {
            textSize = 13f
            setTextColor(ink)
            setPadding(dp(12), dp(10), dp(12), dp(10))
            background = rounded(accentSoft, Color.TRANSPARENT)
            setTextIsSelectable(true)
        }
        status = TextView(this).apply {
            textSize = 13f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(dp(12), dp(8), dp(12), dp(8))
        }
        bundleState = TextView(this).apply {
            textSize = 13f
            setTextColor(muted)
        }

        val autostart = CheckBox(this).apply {
            text = "Start on boot"
            isChecked = prefs.getBoolean("autostart", true)
            setTextColor(ink)
            textSize = 14f
            setOnCheckedChangeListener { _, checked ->
                prefs.edit().putBoolean("autostart", checked).apply()
            }
        }
        logs = TextView(this).apply {
            textSize = 12f
            setTextColor(ink)
            setTextIsSelectable(true)
            setPadding(dp(12), dp(10), dp(12), dp(10))
            text = recentLogText()
        }
        relayToggle = button("Start relay", primary = true) {
            toggleRelay()
        }
        val logScroll = ScrollView(this).apply {
            background = rounded(Color.rgb(249, 250, 251), line)
            isVerticalScrollBarEnabled = true
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(180))
            addView(logs)
        }

        content.addView(title("TunnelDesktop Relay"))
        addSpaced(content, status, 10)
        addSpaced(
            content,
            section(
                "Relay Endpoint",
                field("Relay address", relayAddr),
                publicIpv6,
                actionRow(
                    button("Detect IPv6") { refreshPublicIpv6() },
                    button("Use IPv6") { applyPublicIpv6() }
                ),
                actionRow(
                    button("Copy IPv6") { copyPublicIpv6() },
                    button("Refresh") { refreshStatus() }
                )
            )
        )
        addSpaced(
            content,
            section(
                "Bundles",
                field("Certificate names", relayHosts),
                field("Work agent HTTP proxy", agentProxy),
                field("Hotspot allowlist", rawAllow),
                button("Generate bundles", primary = true) { generateIdentity() },
                bundleState,
                actionRow(
                    button("Export agent") { shareBundle("agent.tnl", prefs.getString("agent_bundle", "") ?: "") },
                    button("Export client") { shareBundle("client.tnl", prefs.getString("client_bundle", "") ?: "") }
                )
            )
        )
        addSpaced(
            content,
            section(
                "Relay Service",
                relayToggle,
                autostart,
                actionRow(
                    button("Battery") { requestBatteryExemption() },
                    button("Unrestricted") { openAppBatterySettings() }
                ),
                button("VPN persistence") { requestVpnPersistence() }
            )
        )
        addSpaced(
            content,
            section("Activity Log", logScroll)
        )
        setContentView(root)
    }

    private fun generateIdentity() {
        try {
            val options = JSONObject()
                .put("relay_addr", relayAddr.text.toString().trim())
                .put("relay_hosts", jsonArray(certificateNames()))
                .put("agent_proxy", agentProxyValue())
                .put("raw_rdp_addr", "0.0.0.0:3389")
                .put("raw_allowlist", jsonArray(rawAllowlist()))
                .put("rdp_addr", "127.0.0.1:3389")
                .put("client_listen", "127.0.0.1:3389")
            val result = JSONObject(Relaycore.generateSetup(options.toString()))
            prefs.edit()
                .putString("relay_addr", relayAddr.text.toString().trim())
                .putString("relay_hosts", relayHosts.text.toString())
                .putString("agent_proxy", agentProxy.text.toString().trim())
                .putString("raw_allow", rawAllow.text.toString())
                .putString("config", result.getString("relay_config_json"))
                .putString("agent_bundle", result.getString("agent_bundle"))
                .putString("client_bundle", result.getString("client_bundle"))
                .putBoolean("autostart", true)
                .apply()
            refreshStatus("Generated bundles")
            Toast.makeText(this, "Bundles generated", Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            refreshStatus("Generate failed: $message")
            Toast.makeText(this, "Generate failed: $message", Toast.LENGTH_LONG).show()
        }
    }

    private fun shareBundle(name: String, bundle: String) {
        if (bundle.isBlank()) {
            refreshStatus("Generate identity first")
            return
        }
        startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_SUBJECT, name)
            putExtra(Intent.EXTRA_TEXT, bundle)
        }, "Export $name"))
    }

    private fun toggleRelay() {
        val relay = Relaycore.status()
        val requestedRunning = prefs.getBoolean("running", false)
        val lastError = prefs.getString("last_error", "") ?: ""
        if (relay == "running" || requestedRunning && lastError.isBlank()) {
            stopRelay()
        } else {
            startRelay()
        }
    }

    private fun startRelay() {
        try {
            prefs.edit()
                .remove("last_error")
                .putBoolean("running", true)
                .apply()
            val intent = Intent(this, RelayService::class.java)
            if (Build.VERSION.SDK_INT >= 26) {
                startForegroundService(intent)
            } else {
                startService(intent)
            }
            refreshStatus("Starting relay")
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            prefs.edit()
                .putBoolean("running", false)
                .putString("last_error", message)
                .apply()
            refreshStatus("Start failed: $message")
            Toast.makeText(this, "Start failed: $message", Toast.LENGTH_LONG).show()
        }
    }

    private fun stopRelay() {
        try {
            stopService(Intent(this, RelayService::class.java))
            prefs.edit()
                .putBoolean("running", false)
                .remove("last_error")
                .apply()
            refreshStatus("Relay stopped")
        } catch (e: Exception) {
            val message = e.message ?: e.javaClass.simpleName
            refreshStatus("Stop failed: $message")
            Toast.makeText(this, "Stop failed: $message", Toast.LENGTH_LONG).show()
        }
    }

    private fun refreshStatus(extra: String = "") {
        refreshPublicIpv6()
        val hasConfig = !prefs.getString("config", "").isNullOrBlank()
        val hasAgent = !prefs.getString("agent_bundle", "").isNullOrBlank()
        val hasClient = !prefs.getString("client_bundle", "").isNullOrBlank()
        val relay = Relaycore.status()
        val requestedRunning = prefs.getBoolean("running", false)
        val lastError = prefs.getString("last_error", "") ?: ""
        val statusModel = when {
            relay == "running" -> StatusModel("Relay running", success, Color.rgb(230, 246, 237))
            requestedRunning && lastError.isBlank() -> StatusModel("Relay starting", accent, accentSoft)
            lastError.isNotBlank() -> StatusModel("Relay start failed", danger, Color.rgb(252, 232, 232))
            else -> StatusModel("Relay stopped", muted, Color.rgb(239, 242, 245))
        }
        status.text = statusModel.text
        status.setTextColor(statusModel.textColor)
        status.background = rounded(statusModel.backgroundColor, Color.TRANSPARENT)
        updateRelayToggle(relay, requestedRunning, lastError)
        bundleState.text = listOf(
            if (hasConfig) "Relay config ready" else "Relay config missing",
            if (hasAgent) "Agent bundle ready" else "Agent bundle missing",
            if (hasClient) "Client bundle ready" else "Client bundle missing",
            if (requestedRunning && relay != "running" && lastError.isBlank()) "Waiting for relay service to report running" else "",
            if (lastError.isNotBlank()) "Last start error: $lastError" else "",
            extra
        ).filter { it.isNotBlank() }.joinToString("\n")
    }

    private fun updateRelayToggle(relay: String, requestedRunning: Boolean, lastError: String) {
        if (relay == "running" || requestedRunning && lastError.isBlank()) {
            relayToggle.text = "Stop relay"
            styleButton(relayToggle, danger = true)
        } else {
            relayToggle.text = "Start relay"
            styleButton(relayToggle, primary = true)
        }
    }

    private fun refreshPublicIpv6() {
        val candidates = publicIpv6Candidates()
        val port = relayPort()
        publicIpv6.text = if (candidates.isEmpty()) {
            "Public IPv6: none detected"
        } else {
            val primary = candidates.first()
            val lines = mutableListOf(
                "Public IPv6: ${primary.address} (${primary.interfaceName})",
                "Agent relay address: [${primary.address}]:$port"
            )
            if (candidates.size > 1) {
                lines.add("Other IPv6: " + candidates.drop(1).joinToString(", ") { "${it.address} (${it.interfaceName})" })
            }
            lines.joinToString("\n")
        }
    }

    private fun applyPublicIpv6() {
        val candidate = publicIpv6Candidates().firstOrNull()
        if (candidate == null) {
            refreshStatus("No public IPv6 detected")
            return
        }
        val port = relayPort()
        relayAddr.setText("[${candidate.address}]:$port")
        val hosts = relayHosts.text.toString()
            .split(",")
            .map { it.trim() }
            .filter { it.isNotEmpty() && it != candidate.address }
        relayHosts.setText((listOf(candidate.address) + hosts).joinToString(","))
        prefs.edit()
            .putString("relay_addr", relayAddr.text.toString())
            .putString("relay_hosts", relayHosts.text.toString())
            .apply()
        refreshStatus("IPv6 applied; regenerate bundles")
    }

    private fun copyPublicIpv6() {
        val candidate = publicIpv6Candidates().firstOrNull()
        if (candidate == null) {
            refreshStatus("No public IPv6 detected")
            return
        }
        val relay = "[${candidate.address}]:${relayPort()}"
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("TunnelDesktop relay address", relay))
        Toast.makeText(this, "Copied $relay", Toast.LENGTH_SHORT).show()
    }

    private fun publicIpv6Candidates(): List<PublicIpv6> {
        return try {
            Collections.list(NetworkInterface.getNetworkInterfaces())
                .filter { it.isUp && !it.isLoopback }
                .flatMap { iface ->
                    Collections.list(iface.inetAddresses)
                        .filterIsInstance<Inet6Address>()
                        .mapNotNull { address ->
                            val host = address.hostAddress?.substringBefore("%") ?: return@mapNotNull null
                            if (isPublicIpv6(address)) PublicIpv6(iface.name, host) else null
                        }
                }
                .distinctBy { it.address }
        } catch (_: Exception) {
            emptyList()
        }
    }

    private fun isPublicIpv6(address: Inet6Address): Boolean {
        if (address.isAnyLocalAddress || address.isLoopbackAddress || address.isLinkLocalAddress) return false
        if (address.isSiteLocalAddress || address.isMulticastAddress) return false
        val first = address.address[0].toInt() and 0xff
        if ((first and 0xfe) == 0xfc) return false
        return true
    }

    private fun relayPort(): String {
        val value = relayAddr.text.toString().trim()
        if (value.startsWith("[")) {
            val end = value.indexOf("]")
            if (end >= 0 && value.length > end + 2 && value[end + 1] == ':') {
                return value.substring(end + 2)
            }
            return "443"
        }
        return if (value.count { it == ':' } == 1) value.substringAfterLast(":") else "443"
    }

    private fun certificateNames(): List<String> {
        return relayHosts.text.toString()
            .split(",")
            .map { normalizeCertificateName(it.trim()) }
            .filter { it.isNotEmpty() }
    }

    private fun rawAllowlist(): List<String> {
        return rawAllow.text.toString()
            .split(",")
            .map { it.trim() }
            .filter { it.isNotEmpty() }
    }

    private fun jsonArray(values: List<String>): JSONArray {
        return JSONArray().apply {
            values.forEach { put(it) }
        }
    }

    private fun normalizeCertificateName(value: String): String {
        if (value.startsWith("[")) {
            val end = value.indexOf("]")
            if (end > 0) return value.substring(1, end)
        }
        return if (value.count { it == ':' } == 1) value.substringBefore(":") else value
    }

    private fun agentProxyValue(): String {
        val value = agentProxy.text.toString().trim()
        return if (value.isBlank()) "direct" else value
    }

    private fun savedAgentProxy(): String {
        val value = prefs.getString("agent_proxy", "") ?: ""
        return if (value == "http://PROXY:PORT") "" else value
    }

    private data class PublicIpv6(val interfaceName: String, val address: String)
    private data class StatusModel(val text: String, val textColor: Int, val backgroundColor: Int)

    private fun recentLogText(): String {
        return try {
            val values = JSONArray(Relaycore.recentLogs())
            (0 until values.length()).joinToString("\n") { values.getString(it) }
        } catch (_: Exception) {
            ""
        }
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

    private fun edit(hintText: String, value: String): EditText =
        EditText(this).apply {
            hint = hintText
            setText(value)
            setSingleLine(true)
            textSize = 14f
            setTextColor(ink)
            setHintTextColor(Color.rgb(130, 140, 150))
            setPadding(dp(12), 0, dp(12), 0)
            minHeight = dp(46)
            background = rounded(Color.WHITE, line)
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
            if (stroke != Color.TRANSPARENT) {
                setStroke(dp(1), stroke)
            }
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
        if (prepare != null) {
            startActivityForResult(prepare, 42)
        } else {
            startService(Intent(this, PersistenceVpnService::class.java))
        }
    }

    private fun deviceProtectedStorageContext() =
        if (Build.VERSION.SDK_INT >= 24) createDeviceProtectedStorageContext() else this
}

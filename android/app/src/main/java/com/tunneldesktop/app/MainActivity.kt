package com.tunneldesktop.app

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.view.Gravity
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
    private lateinit var logs: TextView

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

    private fun buildUi() {
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(28, 28, 28, 28)
        }
        relayAddr = edit("Relay hostname:port", prefs.getString("relay_addr", "phone.example.com:443") ?: "")
        relayHosts = edit("Certificate names", prefs.getString("relay_hosts", "phone.example.com") ?: "")
        agentProxy = edit("Work agent HTTP proxy (optional)", savedAgentProxy())
        rawAllow = edit("Hotspot allowlist", prefs.getString("raw_allow", "192.168.43.0/24") ?: "")
        publicIpv6 = TextView(this).apply {
            textSize = 14f
            setTextIsSelectable(true)
        }
        status = TextView(this).apply { textSize = 14f }

        val row1 = row()
        row1.addView(button("Generate") { generateIdentity() })
        row1.addView(button("Start") {
            startForegroundService(Intent(this, RelayService::class.java))
            prefs.edit().putBoolean("running", true).apply()
            refreshStatus()
        })
        row1.addView(button("Stop") {
            stopService(Intent(this, RelayService::class.java))
            prefs.edit().putBoolean("running", false).apply()
            refreshStatus()
        })

        val row2 = row()
        row2.addView(button("Agent") { shareBundle("agent.tnl", prefs.getString("agent_bundle", "") ?: "") })
        row2.addView(button("Client") { shareBundle("client.tnl", prefs.getString("client_bundle", "") ?: "") })
        row2.addView(button("Battery") { requestBatteryExemption() })

        val row3 = row()
        row3.addView(button("Unrestricted") { openAppBatterySettings() })
        row3.addView(button("VPN") { requestVpnPersistence() })
        row3.addView(button("Status") { refreshStatus() })

        val row4 = row()
        row4.addView(button("Detect IPv6") { refreshPublicIpv6() })
        row4.addView(button("Use IPv6") { applyPublicIpv6() })
        row4.addView(button("Copy IPv6") { copyPublicIpv6() })

        val autostart = CheckBox(this).apply {
            text = "Start on boot"
            isChecked = prefs.getBoolean("autostart", true)
            setOnCheckedChangeListener { _, checked ->
                prefs.edit().putBoolean("autostart", checked).apply()
            }
        }
        logs = TextView(this).apply {
            textSize = 12f
            text = Relaycore.recentLogs().trim('[', ']', '"').replace("\\n", "\n")
        }
        val scroll = ScrollView(this).apply { addView(logs) }

        root.addView(relayAddr)
        root.addView(relayHosts)
        root.addView(agentProxy)
        root.addView(rawAllow)
        root.addView(publicIpv6)
        root.addView(row1)
        root.addView(row2)
        root.addView(row3)
        root.addView(row4)
        root.addView(autostart)
        root.addView(status)
        root.addView(scroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)
    }

    private fun generateIdentity() {
        try {
            val options = JSONObject()
                .put("relay_addr", relayAddr.text.toString().trim())
                .put("relay_hosts", certificateNames())
                .put("agent_proxy", agentProxyValue())
                .put("raw_rdp_addr", "0.0.0.0:3389")
                .put("raw_allowlist", rawAllow.text.toString().split(",").map { it.trim() }.filter { it.isNotEmpty() })
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

    private fun refreshStatus(extra: String = "") {
        refreshPublicIpv6()
        val hasConfig = !prefs.getString("config", "").isNullOrBlank()
        val hasAgent = !prefs.getString("agent_bundle", "").isNullOrBlank()
        val hasClient = !prefs.getString("client_bundle", "").isNullOrBlank()
        status.text = listOf(
            "relay=${Relaycore.status()}",
            "config=$hasConfig",
            "agent=$hasAgent",
            "client=$hasClient",
            extra
        ).filter { it.isNotBlank() }.joinToString("  ")
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

    private fun edit(hintText: String, value: String): EditText =
        EditText(this).apply {
            hint = hintText
            setText(value)
            setSingleLine(true)
        }

    private fun row(): LinearLayout =
        LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }

    private fun button(text: String, action: () -> Unit): Button =
        Button(this).apply {
            this.text = text
            setOnClickListener { action() }
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        }

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

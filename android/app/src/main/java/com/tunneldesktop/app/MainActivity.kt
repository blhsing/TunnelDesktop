package com.tunneldesktop.app

import android.Manifest
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
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import org.json.JSONObject
import relaycore.Relaycore

class MainActivity : AppCompatActivity() {
    private lateinit var prefs: SharedPreferences
    private lateinit var relayAddr: EditText
    private lateinit var relayHosts: EditText
    private lateinit var agentProxy: EditText
    private lateinit var rawAllow: EditText
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
        agentProxy = edit("Work proxy", prefs.getString("agent_proxy", "http://PROXY:PORT") ?: "")
        rawAllow = edit("Hotspot allowlist", prefs.getString("raw_allow", "192.168.43.0/24") ?: "")
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
        root.addView(row1)
        root.addView(row2)
        root.addView(row3)
        root.addView(autostart)
        root.addView(status)
        root.addView(scroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)
    }

    private fun generateIdentity() {
        val options = JSONObject()
            .put("relay_addr", relayAddr.text.toString())
            .put("relay_hosts", relayHosts.text.toString().split(",").map { it.trim() }.filter { it.isNotEmpty() })
            .put("agent_proxy", agentProxy.text.toString())
            .put("raw_rdp_addr", "0.0.0.0:3389")
            .put("raw_allowlist", rawAllow.text.toString().split(",").map { it.trim() }.filter { it.isNotEmpty() })
            .put("rdp_addr", "127.0.0.1:3389")
            .put("client_listen", "127.0.0.1:3389")
        val result = JSONObject(Relaycore.generateSetup(options.toString()))
        prefs.edit()
            .putString("relay_addr", relayAddr.text.toString())
            .putString("relay_hosts", relayHosts.text.toString())
            .putString("agent_proxy", agentProxy.text.toString())
            .putString("raw_allow", rawAllow.text.toString())
            .putString("config", result.getString("relay_config_json"))
            .putString("agent_bundle", result.getString("agent_bundle"))
            .putString("client_bundle", result.getString("client_bundle"))
            .putBoolean("autostart", true)
            .apply()
        refreshStatus()
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

package com.tunneldesktop.app

import org.json.JSONObject

object AndroidRelayPorts {
    const val DEFAULT_PORT = 8443
    const val MIN_LISTEN_PORT = 1024

    fun displayPort(portText: String): Int {
        val port = parsePort(portText)
        return if (port != null && port >= MIN_LISTEN_PORT) port else DEFAULT_PORT
    }

    fun requirePort(portText: String): Int {
        val port = parsePort(portText)
            ?: throw IllegalArgumentException("Relay port must be a number, for example $DEFAULT_PORT")
        requireAllowedPort(port)
        return port
    }

    fun endpoint(hostInput: String, portText: String): String {
        val host = normalizeHost(hostInput)
        if (host.isBlank()) {
            throw IllegalArgumentException("Relay host or IPv6 address is required")
        }
        val port = requirePort(portText)
        return if (host.contains(":")) "[$host]:$port" else "$host:$port"
    }

    fun normalizeHost(hostInput: String): String {
        val value = hostInput.trim()
        if (value.startsWith("[")) {
            val end = value.indexOf("]")
            if (end > 0) {
                return value.substring(1, end)
            }
        }
        if (value.count { it == ':' } == 1) {
            return value.substringBeforeLast(":")
        }
        return value
    }

    fun requireConfigListenPort(configJSON: String): Int {
        val listenAddr = JSONObject(configJSON).optString("listen_addr", "").ifBlank { ":$DEFAULT_PORT" }
        val port = portFromAddress(listenAddr)
            ?: throw IllegalStateException("Relay config listen_addr must include a port")
        requireAllowedPort(port)
        return port
    }

    private fun requireAllowedPort(port: Int) {
        if (port !in 1..65535) {
            throw IllegalArgumentException("Relay port must be between 1 and 65535")
        }
        if (port < MIN_LISTEN_PORT) {
            throw IllegalStateException(
                "Android cannot listen on port $port without root. Use port $DEFAULT_PORT or another port 1024-65535, then regenerate bundles."
            )
        }
    }

    private fun portFromAddress(address: String): Int? {
        val value = address.trim()
        if (value.startsWith("[")) {
            val end = value.indexOf("]")
            if (end >= 0 && value.length > end + 2 && value[end + 1] == ':') {
                return parsePort(value.substring(end + 2))
            }
            return null
        }
        if (value.count { it == ':' } == 1) {
            return parsePort(value.substringAfterLast(":"))
        }
        if (value.startsWith(":")) {
            return parsePort(value.substring(1))
        }
        return null
    }

    private fun parsePort(portText: String): Int? {
        val port = portText.trim().toIntOrNull() ?: return null
        return if (port in 1..65535) port else null
    }
}

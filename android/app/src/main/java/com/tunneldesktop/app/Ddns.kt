package com.tunneldesktop.app

import java.net.HttpURLConnection
import java.net.URL

object Ddns {
    fun update(template: String, ipv6: String): Int {
        val url = URL(template.replace("{ipv6}", ipv6))
        val conn = url.openConnection() as HttpURLConnection
        conn.connectTimeout = 10_000
        conn.readTimeout = 10_000
        conn.requestMethod = "GET"
        return conn.responseCode
    }
}

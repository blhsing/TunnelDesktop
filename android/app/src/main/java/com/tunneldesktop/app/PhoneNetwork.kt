package com.tunneldesktop.app

import java.net.Inet4Address
import java.net.NetworkInterface
import java.util.Collections

data class PrivateIp(val interfaceName: String, val address: String)

object PhoneNetwork {
    fun hotspotIp(): String? = privateIpv4Candidates().firstOrNull()?.address

    fun privateIpSummary(): String =
        privateIpv4Candidates().joinToString(", ") { "${it.address} (${it.interfaceName})" }

    fun privateIpv4Candidates(): List<PrivateIp> {
        return try {
            Collections.list(NetworkInterface.getNetworkInterfaces())
                .filter { it.isUp && !it.isLoopback }
                .flatMap { iface ->
                    iface.interfaceAddresses.mapNotNull { interfaceAddress ->
                        val address = interfaceAddress.address
                        if (address is Inet4Address) {
                            val host = address.hostAddress ?: return@mapNotNull null
                            if (isPrivateIpv4(address)) PrivateIp(iface.name, host) else null
                        } else {
                            null
                        }
                    }
                }
                .distinctBy { it.address }
                .sortedWith(compareBy<PrivateIp> { privateIpRank(it.interfaceName, it.address) }.thenBy { it.interfaceName }.thenBy { it.address })
        } catch (_: Exception) {
            emptyList()
        }
    }

    private fun isPrivateIpv4(address: Inet4Address): Boolean {
        if (address.isAnyLocalAddress || address.isLoopbackAddress || address.isMulticastAddress) return false
        val bytes = address.address.map { it.toInt() and 0xff }
        return bytes[0] == 10 ||
            bytes[0] == 172 && bytes[1] in 16..31 ||
            bytes[0] == 192 && bytes[1] == 168
    }

    private fun privateIpRank(interfaceName: String, address: String): Int {
        val name = interfaceName.lowercase()
        return when {
            name.startsWith("ap") || name.contains("hotspot") || name.startsWith("swlan") -> 0
            address.startsWith("192.168.43.") || address.startsWith("192.168.203.") -> 1
            name.startsWith("wlan") || name.startsWith("wifi") -> 2
            name.startsWith("rndis") || name.startsWith("eth") || name.contains("usb") -> 3
            else -> 4
        }
    }
}

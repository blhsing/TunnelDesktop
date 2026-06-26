package com.tunneldesktop.app

import android.net.VpnService
import android.os.ParcelFileDescriptor

class PersistenceVpnService : VpnService() {
    private var tun: ParcelFileDescriptor? = null

    override fun onCreate() {
        super.onCreate()
        tun = Builder()
            .setSession("TunnelDesktop Persistence")
            .addAddress("10.250.0.1", 32)
            .establish()
    }

    override fun onDestroy() {
        tun?.close()
        tun = null
        super.onDestroy()
    }
}

package com.blhsing.tunneldesktop.home;

import android.content.Context;
import android.content.SharedPreferences;

final class HomePrefs {
    static final String PREFS = "tunneldesktop_home";
    static final String PREF_RELAY_URL = "relay_url";
    static final String PREF_LOCAL_PORT = "local_port";
    static final int DEFAULT_LOCAL_PORT = 3389;

    private static final int LEGACY_DEFAULT_LOCAL_PORT = 3390;
    private static final String PREF_DEFAULT_PORT_MIGRATED = "default_port_3389_migrated";

    private HomePrefs() {
    }

    static String loadRelayUrl(Context context) {
        return prefs(context).getString(PREF_RELAY_URL, RelayUrls.DEFAULT_RELAY_URL);
    }

    static int loadLocalPort(Context context) {
        SharedPreferences prefs = prefs(context);
        int port = prefs.getInt(PREF_LOCAL_PORT, DEFAULT_LOCAL_PORT);
        if (!prefs.getBoolean(PREF_DEFAULT_PORT_MIGRATED, false)) {
            SharedPreferences.Editor editor = prefs.edit().putBoolean(PREF_DEFAULT_PORT_MIGRATED, true);
            if (port == LEGACY_DEFAULT_LOCAL_PORT) {
                port = DEFAULT_LOCAL_PORT;
                editor.putInt(PREF_LOCAL_PORT, port);
            }
            editor.apply();
        }
        return sanitizePort(port);
    }

    static void save(Context context, String relayUrl, int port) {
        prefs(context)
                .edit()
                .putString(PREF_RELAY_URL, relayUrl)
                .putInt(PREF_LOCAL_PORT, sanitizePort(port))
                .putBoolean(PREF_DEFAULT_PORT_MIGRATED, true)
                .apply();
    }

    static int sanitizePort(int port) {
        return port > 0 && port < 65536 ? port : DEFAULT_LOCAL_PORT;
    }

    private static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }
}

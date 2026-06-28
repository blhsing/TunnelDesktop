package com.blhsing.deskferry.home;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.GridLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import java.net.URISyntaxException;
import java.util.List;
import java.util.Locale;

public class MainActivity extends Activity {
    private final BroadcastReceiver stateReceiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            renderState(TunnelService.snapshot());
        }
    };

    private EditText primaryRelayUrlField;
    private EditText fallbackRelayUrlsField;
    private EditText localPortField;
    private TextView tunnelStatus;
    private TextView workStatus;
    private TextView homeStatus;
    private TextView rdpAddress;
    private TextView activeStatus;
    private TextView messageView;
    private TextView logView;
    private Button startButton;
    private String latestRdpAddress = RelayUrls.rdpAddress(HomePrefs.DEFAULT_LOCAL_PORT);

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        maybeRequestNotificationPermission();
        buildUi();
        loadPreferences();
        renderState(TunnelService.snapshot());
    }

    @Override
    protected void onResume() {
        super.onResume();
        IntentFilter filter = new IntentFilter(TunnelService.ACTION_STATE);
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(stateReceiver, filter, Context.RECEIVER_NOT_EXPORTED);
        } else {
            registerReceiver(stateReceiver, filter);
        }
        renderState(TunnelService.snapshot());
    }

    @Override
    protected void onPause() {
        unregisterReceiver(stateReceiver);
        super.onPause();
    }

    private void buildUi() {
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);
        scroll.setBackgroundColor(color("#F5F7F8"));

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(18), dp(18), dp(18), dp(24));
        scroll.addView(root, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));

        LinearLayout header = new LinearLayout(this);
        header.setOrientation(LinearLayout.VERTICAL);
        header.setPadding(0, 0, 0, dp(12));
        root.addView(header);

        TextView title = label("DeskFerry Home", 28, "#1F2933", true);
        title.setLetterSpacing(0);
        header.addView(title);
        TextView subtitle = label("Android RDP tunnel endpoint", 14, "#65717D", false);
        subtitle.setPadding(0, dp(3), 0, 0);
        header.addView(subtitle);

        GridLayout grid = new GridLayout(this);
        grid.setColumnCount(2);
        grid.setUseDefaultMargins(false);
        root.addView(grid, matchWrap());
        tunnelStatus = addStatusTile(grid, "Tunnel", "Stopped");
        workStatus = addStatusTile(grid, "Work Agent", "Unknown");
        homeStatus = addStatusTile(grid, "Home Presence", "Offline");
        activeStatus = addStatusTile(grid, "Streams", "0 active");

        LinearLayout configCard = card();
        configCard.setOrientation(LinearLayout.VERTICAL);
        configCard.setPadding(dp(14), dp(14), dp(14), dp(14));
        root.addView(configCard, cardParams());

        configCard.addView(sectionTitle("Connection"));
        primaryRelayUrlField = field("Primary relay URL");
        configCard.addView(primaryRelayUrlField, matchWrap());
        fallbackRelayUrlsField = multiLineField("Fallback relay URLs");
        configCard.addView(fallbackRelayUrlsField, matchWrap());
        localPortField = field("Local RDP port");
        localPortField.setInputType(InputType.TYPE_CLASS_NUMBER);
        configCard.addView(localPortField, matchWrap());

        rdpAddress = label(RelayUrls.rdpAddress(HomePrefs.DEFAULT_LOCAL_PORT), 20, "#1F2933", true);
        rdpAddress.setPadding(0, dp(10), 0, 0);
        configCard.addView(rdpAddress);

        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.VERTICAL);
        actions.setPadding(0, dp(12), 0, 0);
        configCard.addView(actions);

        LinearLayout row1 = actionRow();
        startButton = primaryButton("Start Tunnel");
        startButton.setOnClickListener(v -> toggleTunnel());
        row1.addView(startButton, weightedButton());
        Button copy = secondaryButton("Copy RDP Target");
        copy.setOnClickListener(v -> copyRdpTarget());
        row1.addView(copy, weightedButton());
        actions.addView(row1);

        LinearLayout row2 = actionRow();
        Button openRdp = secondaryButton("Open RDP App");
        openRdp.setOnClickListener(v -> openRdpApp());
        row2.addView(openRdp, weightedButton());
        Button dashboard = secondaryButton("Dashboard");
        dashboard.setOnClickListener(v -> openDashboard());
        row2.addView(dashboard, weightedButton());
        actions.addView(row2);

        LinearLayout statusCard = card();
        statusCard.setOrientation(LinearLayout.VERTICAL);
        statusCard.setPadding(dp(14), dp(14), dp(14), dp(14));
        root.addView(statusCard, cardParams());
        statusCard.addView(sectionTitle("Activity"));
        messageView = label("Ready.", 15, "#1F2933", true);
        messageView.setPadding(0, dp(4), 0, dp(8));
        statusCard.addView(messageView);
        logView = label("", 13, "#65717D", false);
        logView.setTypeface(Typeface.MONOSPACE);
        logView.setLineSpacing(0, 1.08f);
        statusCard.addView(logView);

        setContentView(scroll);
    }

    private TextView addStatusTile(GridLayout grid, String title, String initial) {
        LinearLayout tile = card();
        tile.setOrientation(LinearLayout.VERTICAL);
        tile.setPadding(dp(12), dp(12), dp(12), dp(12));

        TextView label = label(title.toUpperCase(Locale.ROOT), 12, "#65717D", true);
        tile.addView(label);
        TextView value = label(initial, 22, "#1F2933", true);
        value.setPadding(0, dp(8), 0, 0);
        tile.addView(value);

        GridLayout.LayoutParams params = new GridLayout.LayoutParams();
        params.width = 0;
        params.height = ViewGroup.LayoutParams.WRAP_CONTENT;
        params.columnSpec = GridLayout.spec(GridLayout.UNDEFINED, 1f);
        params.setMargins(dp(4), dp(4), dp(4), dp(8));
        grid.addView(tile, params);
        return value;
    }

    private void loadPreferences() {
        List<String> relayUrls;
        try {
            relayUrls = RelayUrls.normalizeRelayUrls(HomePrefs.loadRelayUrl(this));
        } catch (URISyntaxException ex) {
            relayUrls = java.util.Collections.singletonList(RelayUrls.DEFAULT_RELAY_URL);
        }
        primaryRelayUrlField.setText(relayUrls.get(0));
        fallbackRelayUrlsField.setText(RelayUrls.joinRelayUrls(relayUrls.subList(1, relayUrls.size())));
        localPortField.setText(String.valueOf(HomePrefs.loadLocalPort(this)));
    }

    private void savePreferences(String relayUrl, int port) {
        HomePrefs.save(this, relayUrl, port);
    }

    private void toggleTunnel() {
        TunnelService.State state = TunnelService.snapshot();
        if (state.running) {
            stopService(new Intent(this, TunnelService.class).setAction(TunnelService.ACTION_STOP));
            return;
        }
        String relayUrl;
        int port;
        try {
            List<String> relayUrls = new java.util.ArrayList<>();
            String primaryRelayUrl = primaryRelayUrlField.getText().toString().trim();
            if (!primaryRelayUrl.isEmpty()) {
                relayUrls.add(RelayUrls.normalizeRelayUrl(primaryRelayUrl));
            }
            relayUrls.addAll(RelayUrls.normalizeRelayUrls(fallbackRelayUrlsField.getText().toString(), false));
            if (relayUrls.isEmpty()) {
                relayUrls.add(RelayUrls.DEFAULT_RELAY_URL);
            }
            relayUrl = RelayUrls.joinRelayUrls(relayUrls);
            port = parsePort(localPortField.getText().toString());
        } catch (Exception ex) {
            Toast.makeText(this, ex.getMessage(), Toast.LENGTH_LONG).show();
            return;
        }
        List<String> relayUrls;
        try {
            relayUrls = RelayUrls.normalizeRelayUrls(relayUrl);
        } catch (URISyntaxException ex) {
            relayUrls = java.util.Collections.singletonList(RelayUrls.DEFAULT_RELAY_URL);
        }
        primaryRelayUrlField.setText(relayUrls.get(0));
        fallbackRelayUrlsField.setText(RelayUrls.joinRelayUrls(relayUrls.subList(1, relayUrls.size())));
        localPortField.setText(String.valueOf(port));
        savePreferences(relayUrl, port);

        Intent intent = new Intent(this, TunnelService.class)
                .setAction(TunnelService.ACTION_START)
                .putExtra(TunnelService.EXTRA_RELAY_URL, relayUrl)
                .putExtra(TunnelService.EXTRA_LOCAL_PORT, port);
        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(intent);
        } else {
            startService(intent);
        }
    }

    private int parsePort(String value) {
        int port = Integer.parseInt(value.trim());
        if (port <= 0 || port > 65535) {
            throw new IllegalArgumentException("Local RDP port must be 1-65535.");
        }
        return port;
    }

    private void renderState(TunnelService.State state) {
        latestRdpAddress = state.rdpAddress;
        tunnelStatus.setText(state.tunnelStatus);
        workStatus.setText(state.workStatus);
        homeStatus.setText(state.homeStatus);
        activeStatus.setText(state.activeConnections + " active");
        rdpAddress.setText(state.rdpAddress);
        messageView.setText(state.lastMessage);
        logView.setText(state.log);
        startButton.setText(state.running ? "Stop Tunnel" : "Start Tunnel");
        primaryRelayUrlField.setEnabled(!state.running);
        fallbackRelayUrlsField.setEnabled(!state.running);
        localPortField.setEnabled(!state.running);
    }

    private void copyRdpTarget() {
        ClipboardManager clipboard = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
        clipboard.setPrimaryClip(ClipData.newPlainText("DeskFerry RDP target", latestRdpAddress));
        Toast.makeText(this, "Copied " + latestRdpAddress, Toast.LENGTH_SHORT).show();
    }

    private void openRdpApp() {
        Uri uri = Uri.parse("rdp://" + latestRdpAddress);
        Intent intent = new Intent(Intent.ACTION_VIEW, uri);
        try {
            startActivity(intent);
        } catch (Exception ex) {
            Intent share = new Intent(Intent.ACTION_SEND)
                    .setType("text/plain")
                    .putExtra(Intent.EXTRA_TEXT, latestRdpAddress);
            startActivity(Intent.createChooser(share, "RDP target"));
        }
    }

    private void openDashboard() {
        String relayUrl = primaryRelayUrlField.getText().toString();
        try {
            relayUrl = RelayUrls.normalizeRelayUrl(relayUrl);
        } catch (URISyntaxException ignored) {
        }
        startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(RelayUrls.dashboardUrl(relayUrl))));
    }

    private void maybeRequestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 701);
        }
    }

    private LinearLayout card() {
        LinearLayout view = new LinearLayout(this);
        view.setBackground(rounded("#FFFFFF", "#D7DEE3", 8));
        return view;
    }

    private EditText field(String hint) {
        EditText edit = new EditText(this);
        edit.setSingleLine(true);
        edit.setTextSize(15);
        edit.setHint(hint);
        edit.setPadding(dp(12), 0, dp(12), 0);
        edit.setMinHeight(dp(48));
        edit.setTextColor(color("#1F2933"));
        edit.setHintTextColor(color("#8A949E"));
        edit.setBackground(rounded("#FBFCFD", "#D7DEE3", 8));
        return edit;
    }

    private EditText multiLineField(String hint) {
        EditText edit = field(hint);
        edit.setSingleLine(false);
        edit.setMinLines(2);
        edit.setMaxLines(4);
        edit.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE | InputType.TYPE_TEXT_VARIATION_URI);
        edit.setGravity(Gravity.TOP | Gravity.START);
        edit.setMinHeight(dp(88));
        return edit;
    }

    private TextView sectionTitle(String text) {
        TextView view = label(text, 17, "#1F2933", true);
        view.setPadding(0, 0, 0, dp(10));
        return view;
    }

    private TextView label(String text, int sp, String color, boolean bold) {
        TextView view = new TextView(this);
        view.setText(text);
        view.setTextSize(sp);
        view.setTextColor(color(color));
        view.setIncludeFontPadding(true);
        if (bold) {
            view.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        }
        return view;
    }

    private Button primaryButton(String text) {
        Button button = button(text);
        button.setTextColor(Color.WHITE);
        button.setBackground(rounded("#2F6F73", "#2F6F73", 8));
        return button;
    }

    private Button secondaryButton(String text) {
        Button button = button(text);
        button.setTextColor(color("#2F6F73"));
        button.setBackground(rounded("#FFFFFF", "#9BC7C2", 8));
        return button;
    }

    private Button button(String text) {
        Button button = new Button(this);
        button.setAllCaps(false);
        button.setText(text);
        button.setTextSize(14);
        button.setGravity(Gravity.CENTER);
        button.setMinHeight(dp(44));
        return button;
    }

    private LinearLayout actionRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER);
        row.setPadding(0, 0, 0, dp(8));
        return row;
    }

    private LinearLayout.LayoutParams weightedButton() {
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(0, dp(48), 1f);
        params.setMargins(dp(4), 0, dp(4), 0);
        return params;
    }

    private LinearLayout.LayoutParams cardParams() {
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT);
        params.setMargins(0, dp(8), 0, dp(10));
        return params;
    }

    private LinearLayout.LayoutParams matchWrap() {
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT);
        params.setMargins(0, 0, 0, dp(10));
        return params;
    }

    private GradientDrawable rounded(String fill, String stroke, int radiusDp) {
        GradientDrawable drawable = new GradientDrawable();
        drawable.setColor(color(fill));
        drawable.setCornerRadius(dp(radiusDp));
        drawable.setStroke(dp(1), color(stroke));
        return drawable;
    }

    private int color(String hex) {
        return Color.parseColor(hex);
    }

    private int dp(float value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }
}

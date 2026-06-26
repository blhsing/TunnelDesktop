# TunnelDesktop

TunnelDesktop is an outbound-only RDP rendezvous tunnel for a work PC that cannot accept inbound connections. The current architecture uses a .NET Azure App Service relay at `https://test-officialwebsite.azurewebsites.net/relay/`. The work agent, home client, and Android home agent all connect out to that service over HTTPS WebSockets.

Configuration is a single shared relay room URL. For example:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The room name is the path segment after `/relay/`. The first agent to use a room creates it in memory, and later agents join the same room by using the same URL.

## Table Of Contents

- [How It Works](#how-it-works)
- [Installation](#installation)
  - [1. Deploy Azure Relay](#1-deploy-azure-relay)
  - [2. Choose A Room URL](#2-choose-a-room-url)
  - [3. Install Work Agent](#3-install-work-agent)
  - [4. Run Home Client](#4-run-home-client)
  - [5. Run Android Home Agent](#5-run-android-home-agent)
- [Deliverables](#deliverables)
  - [Azure Relay Web Service](#azure-relay-web-service)
  - [Work Agent](#work-agent)
  - [Agent Configurator](#agent-configurator)
  - [Home Client](#home-client)
  - [Android Home Agent](#android-home-agent)
  - [Standalone Relay Harness](#standalone-relay-harness)
- [Security Model](#security-model)
- [Build Prerequisites](#build-prerequisites)
- [Build Commands](#build-commands)
- [URL Configuration](#url-configuration)
- [Troubleshooting](#troubleshooting)
  - [Agent Self-Test Fails Through Proxy](#agent-self-test-fails-through-proxy)
  - [Client Connects But RDP Fails](#client-connects-but-rdp-fails)
  - [RDP To The Android Phone IP Times Out](#rdp-to-the-android-phone-ip-times-out)
  - [Android Home Agent Does Not Stay Connected](#android-home-agent-does-not-stay-connected)
  - [Azure Relay Status](#azure-relay-status)
  - [Gradle Cannot Download Dependencies](#gradle-cannot-download-dependencies)
- [Development](#development)
- [Repository Layout](#repository-layout)
- [Status](#status)
- [Current Limitations](#current-limitations)

## How It Works

```text
Home PC
  mstsc -> 127.0.0.1:<client port>
        |
        v
client.exe -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
  outbound WebSocket over HTTPS
        |
        v
Azure App Service relay
  dashboard: https://test-officialwebsite.azurewebsites.net/relay/workdesk
  data:      wss://test-officialwebsite.azurewebsites.net/relay/workdesk/ws
        |
        v
agent.exe Windows service
  outbound WebSocket over HTTPS, optionally through HTTP proxy
  per paired socket -> 127.0.0.1:3389
```

The relay groups sockets by room name. A waiting work-agent socket is paired with one home-client socket from the same room, then the relay copies binary WebSocket frames in both directions. The relay does not store credentials or generated client files.

## Installation

### 1. Deploy Azure Relay

Build the deployable zip:

```powershell
.\build\build-azure-relay.ps1
```

Deploy `dist\azure-relay\tunneldesktop-azure-relay.zip` to the Azure App Service. Confirm WebSockets are enabled in App Service configuration.

Dashboard and health endpoints:

```text
https://test-officialwebsite.azurewebsites.net/relay/
https://test-officialwebsite.azurewebsites.net/relay/<room>
https://test-officialwebsite.azurewebsites.net/relay/health
https://test-officialwebsite.azurewebsites.net/relay/status
```

### 2. Choose A Room URL

Pick a room name that is easy for you to remember but not obvious to outsiders:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

Use exactly the same URL on the work agent, home client, and Android home agent.

### 3. Install Work Agent

Run the configurator:

```text
agent-configurator-windows-amd64.exe
```

It defaults to `D:\TunnelDesktop\Agent` when `D:` exists. Select `agent-windows-amd64.exe`, enter the relay room URL, then click `Install / Update`. The configurator copies the work agent as `agent.exe`, installs or updates the automatic `TunnelDesktopAgent` Windows service, configures SCM restart recovery, and starts the service.

Command-line install is also supported:

```powershell
.\agent-windows-amd64.exe -install -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

Useful checks:

```powershell
.\agent-windows-amd64.exe -status
.\agent-windows-amd64.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

WebSocket mode uses standard proxy environment variables by default, such as `HTTP_PROXY` and `HTTPS_PROXY`. Use `-proxy http://proxy.example:8080` to force a proxy, or `-proxy direct` to bypass proxy discovery.

### 4. Run Home Client

Start the tray helper with the same room URL:

```powershell
.\client-windows-amd64.exe -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

Use the tray `Connect` item and wait for Remote Desktop to open. The client listens on `127.0.0.1:3389` by default and opens one outbound WebSocket to Azure for each local RDP session. WebSocket mode uses standard proxy environment variables by default.

Console debug mode:

```powershell
.\client-windows-amd64.exe -console -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

### 5. Run Android Home Agent

Install the APK, open TunnelDesktop, enter the same relay room URL, and tap `Start`. The Android app keeps an outbound status connection to Azure and provides copy buttons for the work-agent and home-client commands. It does not listen for inbound RDP or proxy traffic.

## Deliverables

### Azure Relay Web Service

`azure-relay/` is a .NET 8 minimal ASP.NET Core service. It exposes:

- `GET /relay/` for the live overview dashboard.
- `GET /relay/{room}` for a room-scoped live dashboard.
- `GET /relay/health` for machine-readable health.
- `GET /relay/status` for JSON status.
- `GET /relay/ws` and `GET /relay/{room}/ws` as WebSocket endpoints.

WebSocket clients identify their role with:

```text
X-TunnelDesktop-Role: agent | client | home-agent | probe | dashboard
```

Roles:

- `agent`: work-side idle socket waiting to be paired.
- `client`: home-side data socket for one RDP connection.
- `home-agent`: Android outbound status presence.
- `probe`: self-test connection.
- `dashboard`: browser status stream.

The dashboard shows work-agent presence, Android home-agent presence, active stream counts, and the phone's private IPv4 address reported by the Android app. The phone IP is status-only; the Android app does not listen for RDP.

### Work Agent

`agent.exe` is the work-side Windows component. It is Windows-service-first because RDP must work while the user is logged out.

Default behavior:

- `agent.exe` with no args uses the default relay overview URL.
- `-relay-url <url>` selects a named room.
- The service keeps a small pool of idle outbound WebSockets to Azure.
- When Azure pairs a socket, the agent dials `127.0.0.1:3389` and pipes bytes.

Debug and operations:

```powershell
.\agent.exe -console -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\agent.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\agent.exe -status
.\agent.exe -uninstall
```

### Agent Configurator

`agent-configurator-windows-amd64.exe` is the native Windows setup and service management GUI.

It:

- Prefers `D:\TunnelDesktop\Agent` as the install directory when `D:` exists.
- Copies the selected agent binary to `agent.exe`.
- Installs or updates the automatic `TunnelDesktopAgent` Windows service with the configured relay URL.
- Configures SCM restart recovery.
- Starts, stops, restarts, uninstalls, refreshes status, opens the install folder, and runs `agent.exe -self-test`.

### Home Client

`client.exe` is the secure home-side Windows path. It starts a loopback listener, dials Azure with a WebSocket, and launches `mstsc`.

### Android Home Agent

The Android app:

- Stores a shared relay room URL.
- Keeps an outbound `home-agent` WebSocket for relay presence/status.
- Reports the detected hotspot/private IPv4 address to the relay dashboard.
- Copies the matching work-agent and home-client commands.
- Shows and copies the phone's detected hotspot/private IPv4 address for status visibility.
- Uses a foreground service, boot receiver, watchdog receiver, and optional persistence controls.

### Standalone Relay Harness

`cmd/relay` is retained for local protocol experiments around the older direct TLS/yamux listener. It is not the normal Azure deployment path.

## Security Model

- Work and home endpoints make outbound HTTPS WebSocket connections only.
- The room name in the URL is the pairing key.
- The relay never dials the work PC or home PC.
- The work agent only dials `127.0.0.1:3389` after Azure has paired a same-room home connection.

Choose room names that are not obvious. Anyone who knows the room URL can attempt to join that room.

This software can route around corporate egress controls to expose an internal RDP session. Confirm that use is permitted by workplace policy. This project intentionally does not add anti-monitoring, stealth, or obfuscation behavior.

## Build Prerequisites

Required:

- Go 1.25+.
- .NET SDK 8+ for the Azure relay.
- Android SDK for Android builds.
- JDK 17+.
- Gradle, unless using a wrapper in `android/`.

Useful environment variables:

```powershell
$env:ANDROID_HOME = 'D:\Android\Sdk'
$env:ANDROID_SDK_ROOT = 'D:\Android\Sdk'
$env:JAVA_HOME = 'D:\Scoop\apps\temurin17-jdk\current'
```

## Build Commands

Build Azure relay zip:

```powershell
.\build\build-azure-relay.ps1
```

Build Go binaries:

```powershell
.\build\build-go.ps1
```

Build the debug APK:

```powershell
.\build\build-android.ps1
```

Artifacts:

```text
dist\azure-relay\tunneldesktop-azure-relay.zip
dist\bin\agent-windows-amd64.exe
dist\bin\agent-configurator-windows-amd64.exe
dist\bin\client-windows-amd64.exe
dist\bin\relay-windows-amd64.exe
dist\bin\relay-linux-amd64
dist\bin\relay-linux-arm64
android\app\build\outputs\apk\debug\app-debug.apk
```

## URL Configuration

Use one shared URL:

```text
https://test-officialwebsite.azurewebsites.net/relay/<room>
```

Rules:

- `<room>` is created automatically on first use.
- Reusing the same URL joins the same room.
- The WebSocket endpoint is derived automatically as `/relay/<room>/ws`.
- The base `/relay/` path is an overview dashboard.

## Troubleshooting

### Agent Self-Test Fails Through Proxy

Run:

```powershell
.\agent.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

For Azure mode, self-test checks local RDP and then opens a `probe` WebSocket.

Common causes:

- Corporate proxy blocks `CONNECT test-officialwebsite.azurewebsites.net:443`.
- Proxy requires authentication not supported by the service account.
- Azure App Service WebSockets are disabled.
- The Azure relay has not been deployed or is not running.
- Local RDP is disabled or not listening on `127.0.0.1:3389`.

### Client Connects But RDP Fails

Check:

- The room dashboard shows waiting work-agent sockets.
- Agent service is running.
- Work PC allows RDP.
- The configured Windows account is allowed to log in remotely.
- The local client listen port is not already in use.

### RDP To The Android Phone IP Times Out

This is expected in the Azure relay architecture. The Android app is an outbound status home-agent only; it does not listen for RDP on the phone hotspot/private IP.

Run the Windows home client on the home PC instead:

```powershell
.\client-windows-amd64.exe -relay-url https://test-officialwebsite.azurewebsites.net/relay/b
```

Then connect Remote Desktop to the home client's local listener, normally `127.0.0.1:3389`.

### Android Home Agent Does Not Stay Connected

Check:

- The relay URL uses the same room as the work agent and home client.
- Android has network access.
- Foreground notification permission is granted.
- Battery mode is unrestricted if the device aggressively stops background services.

### Azure Relay Status

Open:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The dashboard receives live status over WebSocket. Useful fields:

- waiting work-agent sockets
- active RDP stream pairs
- Android home-agent presence
- last home-client source address

### Gradle Cannot Download Dependencies

Set proxy environment variables before Android builds:

```powershell
$env:HTTPS_PROXY = 'http://proxy.example:8080'
$env:HTTP_PROXY = 'http://proxy.example:8080'
.\build\build-android.ps1
```

## Development

Run all Go tests:

```powershell
go test ./...
```

Build and test the Azure relay:

```powershell
.\build\build-azure-relay.ps1
```

Rebuild after Windows agent/client changes:

```powershell
.\build\build-go.ps1
```

Rebuild after Android changes:

```powershell
.\build\build-android.ps1
```

## Repository Layout

```text
azure-relay/      .NET Azure App Service WebSocket relay
cmd/agent         Windows service work-side agent
cmd/agent-configurator Windows service setup/configurator GUI
cmd/client        Windows tray helper home-side client
cmd/relay         standalone TLS/yamux harness
internal/tunnel   TLS, auth, CONNECT, WebSocket, yamux, pipe, allowlist
internal/relaycore legacy direct relay core
mobile/relaycore  gomobile wrapper around legacy relaycore
android/          Android URL-based home-agent app
build/            build scripts
```

## Status

This repo currently contains:

- Azure App Service WebSocket relay source and publish script.
- Live dashboard with WebSocket status updates.
- Named-room URL joining under `/relay/<room>`.
- Windows work agent implemented as a Windows service deliverable.
- Windows configurator GUI for installing and managing the work agent service.
- Windows home client implemented as a tray helper deliverable.
- Android app for URL-based home-agent status.
- Build scripts for Go binaries, Azure relay zip, and debug APK.

## Current Limitations

- The Azure relay is a simple in-memory broker. Restarting the App Service disconnects active sessions and clears room status.
- Multiple App Service instances are not supported unless sticky routing or shared broker state is added.
- The Go work agent supports direct, environment, and basic HTTP proxy URLs. NTLM proxy authentication is not implemented in the service.
- Android is not an RDP client; use `client.exe` on the home PC for Remote Desktop.
- Signed Android release packaging is not yet added.

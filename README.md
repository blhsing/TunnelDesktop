# TunnelDesktop

TunnelDesktop is an outbound-only RDP rendezvous tunnel for a work PC that cannot accept inbound connections. The current architecture uses a .NET Azure App Service relay at `https://test-officialwebsite.azurewebsites.net/relay/`. The work-side Windows service and the home-side Windows app both connect out to that service over HTTPS WebSockets.

Configuration is a single shared relay room URL. For example:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The room name is the path segment after `/relay/`. The first endpoint to use a room creates it in memory, and later endpoints join the same room by using the same URL.

## Table Of Contents

- [How It Works](#how-it-works)
- [Installation](#installation)
  - [1. Deploy Azure Relay](#1-deploy-azure-relay)
  - [2. Choose A Room URL](#2-choose-a-room-url)
  - [3. Install Work Agent](#3-install-work-agent)
  - [4. Run Home App](#4-run-home-app)
- [Deliverables](#deliverables)
  - [Azure Relay Web Service](#azure-relay-web-service)
  - [Work Agent](#work-agent)
  - [Agent Configurator](#agent-configurator)
  - [Home App](#home-app)
- [Security Model](#security-model)
- [Build Prerequisites](#build-prerequisites)
- [Build Commands](#build-commands)
- [URL Configuration](#url-configuration)
- [Troubleshooting](#troubleshooting)
  - [Agent Self-Test Fails Through Proxy](#agent-self-test-fails-through-proxy)
  - [Home App Connects But RDP Fails](#home-app-connects-but-rdp-fails)
  - [Azure Relay Status](#azure-relay-status)
- [Development](#development)
- [Repository Layout](#repository-layout)
- [Status](#status)
- [Current Limitations](#current-limitations)

## How It Works

```text
Home PC
  mstsc -> 127.0.0.1:<home app port>
        |
        v
client.exe
  friendly Windows control panel + tray icon
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

The home app also keeps a lightweight `home-agent` presence WebSocket open while it is running. That presence socket lets the Azure dashboard and the home control panel show whether the home side is online; RDP data still flows only when the home app starts a local listener and Remote Desktop connects to it.

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

Use exactly the same URL on the work agent and the home app.

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

### 4. Run Home App

Start the Windows home app with the same room URL:

```powershell
.\client-windows-amd64.exe -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The app opens a friendly control panel and a notification-area icon. Click `Connect` to start the local RDP listener and open Remote Desktop. The default local listener is `127.0.0.1:3390`, avoiding Windows' normal local RDP port `3389`, and the app opens one outbound WebSocket to Azure for each local RDP session.

The home app stores its room URL, local RDP address, and proxy mode in `%APPDATA%\TunnelDesktop\home-client.json`. Console debug mode is still available:

```powershell
.\client-windows-amd64.exe -console -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

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
- `home-agent`: Windows home app status presence.
- `probe`: self-test connection.
- `dashboard`: browser status stream.

The dashboard shows work-agent presence, home-app presence, active stream counts, total stream count, and recent remote addresses.

### Work Agent

`agent.exe` is the work-side Windows component. It is Windows-service-first because RDP must work while the user is logged out.

Default behavior:

- `agent.exe` with no args uses the default relay room URL.
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

### Home App

`client.exe` is the secure home-side Windows path. It provides:

- A polished control panel with relay room URL, local RDP address, proxy mode, status tiles, room details, and activity log.
- A notification-area icon with open, connect, stop, Remote Desktop, and quit actions.
- Persistent home-app presence on the relay dashboard.
- A loopback RDP listener, normally `127.0.0.1:3390`.
- Automatic Remote Desktop launch when the user clicks `Connect`.

## Security Model

- Work and home endpoints make outbound HTTPS WebSocket connections only.
- The room name in the URL is the pairing key.
- The relay never dials the work PC or home PC.
- The work agent only dials `127.0.0.1:3389` after Azure has paired a same-room home connection.
- The home app listens on loopback by default, so other LAN devices cannot connect to the local RDP listener unless the user intentionally changes the listen address.

Choose room names that are not obvious. Anyone who knows the room URL can attempt to join that room.

This software can route around corporate egress controls to expose an internal RDP session. Confirm that use is permitted by workplace policy. This project intentionally does not add anti-monitoring, stealth, or obfuscation behavior.

## Build Prerequisites

Required:

- Go 1.25+.
- .NET SDK 8+ for the Azure relay.
- `rsrc` for Windows GUI manifest resources; `build\build-go.ps1` installs it under `D:\Go\bin` when missing.

This repo has been built with Go installed under `D:\Scoop` and .NET SDK 9.x publishing the relay as `net8.0`.

## Build Commands

Build Azure relay zip:

```powershell
.\build\build-azure-relay.ps1
```

Build Go binaries:

```powershell
.\build\build-go.ps1
```

Artifacts:

```text
dist\azure-relay\tunneldesktop-azure-relay.zip
dist\bin\agent-windows-amd64.exe
dist\bin\agent-configurator-windows-amd64.exe
dist\bin\client-windows-amd64.exe
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
- No generated pairing files are required for the normal Azure WebSocket path.

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

### Home App Connects But RDP Fails

Check:

- The home app status tiles show the same room URL as the work agent.
- The room dashboard shows waiting work-agent sockets.
- The agent service is running.
- Work PC allows RDP.
- The configured Windows account is allowed to log in remotely.
- The home app local listen port is not already in use. If `127.0.0.1:3389` fails on the home PC, use `127.0.0.1:3390`.

### Azure Relay Status

Open:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The dashboard receives live status over WebSocket. Useful fields:

- waiting work-agent sockets
- home-app presence
- active RDP stream pairs
- total stream pairs
- last home-client source address

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

## Repository Layout

```text
azure-relay/      .NET Azure App Service WebSocket relay
cmd/agent         Windows service work-side agent
cmd/agent-configurator Windows service setup/configurator GUI
cmd/client        Windows control-panel/tray home app
internal/tunnel   TLS, auth, CONNECT, WebSocket, yamux, pipe, allowlist
build/            build scripts
```

## Status

This repo currently contains:

- Azure App Service WebSocket relay source and publish script.
- Live dashboard with WebSocket status updates.
- Named-room URL joining under `/relay/<room>`.
- Windows work agent implemented as a Windows service deliverable.
- Windows configurator GUI for installing and managing the work agent service.
- Windows home app implemented as a friendly control-panel and tray deliverable.
- Build scripts for Go binaries and the Azure relay zip.

## Current Limitations

- The Azure relay is a simple in-memory broker. Restarting the App Service disconnects active sessions and clears room status.
- Multiple App Service instances are not supported unless sticky routing or shared broker state is added.
- The Go work agent supports direct, environment, and basic HTTP proxy URLs. NTLM proxy authentication is not implemented in the service.
- The home app is a Windows RDP launcher and tunnel endpoint, not a full RDP client.

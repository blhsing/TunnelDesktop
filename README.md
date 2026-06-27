# DeskFerry

DeskFerry is an outbound-only RDP rendezvous tunnel for a work PC that cannot accept inbound connections. The current architecture uses an Azure App Service relay at `https://test-officialwebsite.azurewebsites.net/relay/`. The primary relay implementation is .NET, and a protocol-compatible Python/FastAPI relay is also available under `relay/python/`. The work-side Windows service and the Windows, macOS, and Android home agents connect out to relay web services over WebSockets.

Home apps connect to one relay room URL at a time. The work agent can connect to one or more relay room URLs at the same time, as long as they use the same room name. For example:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
http://217.142.228.117/relay/workdesk
```

The room name is the path segment after `/relay/`. The first endpoint to use a room creates it in memory on that relay, and later endpoints join the same room by using the same URL.

The Android app is a home-agent client like the Windows and macOS home agents. It is not a phone-hosted relay service.

## Table Of Contents

- [How It Works](#how-it-works)
- [Installation](#installation)
  - [1. Deploy Azure Relay](#1-deploy-azure-relay)
  - [2. Deploy Python Relay On OCI](#2-deploy-python-relay-on-oci)
  - [3. Choose A Room URL](#3-choose-a-room-url)
  - [4. Install Work Agent](#4-install-work-agent)
  - [5. Run Windows Home App](#5-run-windows-home-app)
  - [6. Run macOS Home Agent](#6-run-macos-home-agent)
  - [7. Run Android Home App](#7-run-android-home-app)
- [Deliverables](#deliverables)
  - [Azure Relay Web Service](#azure-relay-web-service)
  - [Python Relay Web Service](#python-relay-web-service)
  - [Work Agent](#work-agent)
  - [Agent Configurator](#agent-configurator)
  - [Windows Home App](#windows-home-app)
  - [macOS Home Agent](#macos-home-agent)
  - [Android Home App](#android-home-app)
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
Home PC, Mac, or Android device
  RDP client -> 127.0.0.1:<home agent port>
        |
        v
DeskFerry home agent
  Windows GUI, macOS CLI, or Android foreground service
  outbound WebSocket over HTTPS
        |
        v
Relay web service
  Azure: https://test-officialwebsite.azurewebsites.net/relay/workdesk
  OCI:   http://217.142.228.117/relay/workdesk
        |
        v
agent.exe Windows service
  outbound WebSockets to one or more relay services, optionally through HTTP proxy
  per paired socket -> 127.0.0.1:3389
```

The relay groups sockets by room name. A waiting work-agent socket is paired with one home-client socket from the same room, then the relay copies binary WebSocket frames in both directions. The relay does not store credentials or generated client files.

The home app also keeps a lightweight `home-agent` presence WebSocket open while it is running. That presence socket lets the relay dashboard and home control panels show whether the home side is online; RDP data still flows only when a home agent starts a local listener and an RDP client connects to it.

The Android home app follows the same model. It runs a foreground service, listens on Android loopback, and lets a separate Android RDP client connect to `127.0.0.1:<port>` on the phone. The phone is not a relay and does not need inbound access from the internet.

## Installation

### 1. Deploy Azure Relay

Build the deployable zip:

```powershell
.\build\build-azure-relay.ps1
```

Deploy `dist\azure-relay\deskferry-azure-relay.zip` to the Azure App Service. Confirm WebSockets are enabled in App Service configuration.

Dashboard and health endpoints:

```text
https://test-officialwebsite.azurewebsites.net/relay/
https://test-officialwebsite.azurewebsites.net/relay/<room>
https://test-officialwebsite.azurewebsites.net/relay/health
https://test-officialwebsite.azurewebsites.net/relay/status
```

### 2. Deploy Python Relay On OCI

The Python relay can also run on a small Linux VM. The current OCI deployment is:

```text
http://217.142.228.117/relay/b
```

It runs as the systemd service `deskferry-relay.service` under `/opt/deskferry/python-relay` and listens on public HTTP port `80`. The OCI security rules must allow inbound TCP `80`, and the VM firewall must allow the `http` service.

Build the normal Python source zip and a Linux/Python 3.9 vendored zip:

```powershell
python -m pip install -r relay\python\requirements-dev.txt
.\build\build-python-relay.ps1
```

Artifacts:

```text
dist\python-relay\deskferry-python-relay.zip
dist\python-relay\deskferry-python-relay-linux-cp39-vendored.zip
```

The vendored zip is for Oracle Linux 9's system Python 3.9 and avoids running `pip` on a low-memory Always Free VM. Deploy by extracting the vendored zip to `/opt/deskferry/python-relay`, setting `PYTHONPATH=/opt/deskferry/python-relay/vendor`, and running:

```text
/usr/bin/python3 -m uvicorn app:app --host 0.0.0.0 --port 80 --proxy-headers
```

### 3. Choose A Room URL

Pick a room name that is easy for you to remember but not obvious to outsiders:

```text
https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

For the OCI relay, the equivalent room URL is:

```text
http://217.142.228.117/relay/workdesk
```

Use the same room name everywhere. The home app uses one relay URL. The work agent can use both URLs so the home app can reach the work PC through either relay service.

### 4. Install Work Agent

Run the configurator:

```text
deskferry-agent-configurator-windows-amd64.exe
```

It defaults to `D:\DeskFerry\Agent` when `D:` exists. Select `deskferry-agent-windows-amd64.exe`, enter one or more relay room URLs, then click `Install / Update`. The configurator copies the work agent as `agent.exe`, installs or updates the automatic `DeskFerryAgent` Windows service, configures SCM restart recovery, and starts the service.

Command-line install is also supported:

```powershell
.\deskferry-agent-windows-amd64.exe -install -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\deskferry-agent-windows-amd64.exe -install -relay-url "https://test-officialwebsite.azurewebsites.net/relay/workdesk;http://217.142.228.117/relay/workdesk"
```

Useful checks:

```powershell
.\deskferry-agent-windows-amd64.exe -status
.\deskferry-agent-windows-amd64.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\deskferry-agent-windows-amd64.exe -self-test -relay-url "https://test-officialwebsite.azurewebsites.net/relay/workdesk;http://217.142.228.117/relay/workdesk"
```

WebSocket mode uses standard proxy environment variables by default, such as `HTTP_PROXY` and `HTTPS_PROXY`. Use `-proxy http://proxy.example:8080` to force a proxy, or `-proxy direct` to bypass proxy discovery.

### 5. Run Windows Home App

Start the Windows home app with the same room URL:

```powershell
.\deskferry-home-windows-amd64.exe -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

The app opens a friendly control panel and a notification-area icon. Click `Connect` to start the local RDP listener and open Remote Desktop. The default local listener is `127.0.0.1:3390`, avoiding Windows' normal local RDP port `3389`, and the app opens one outbound WebSocket to Azure for each local RDP session.

The home app stores its room URL, local RDP address, and proxy mode in `%APPDATA%\DeskFerry\home-client.json`. Console debug mode is still available:

```powershell
.\deskferry-home-windows-amd64.exe -console -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

### 6. Run macOS Home Agent

Choose the binary for your Mac:

```sh
chmod +x ./deskferry-home-macos-arm64
./deskferry-home-macos-arm64 -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk -open-rdp
```

Use `deskferry-home-macos-amd64` on Intel Macs. The macOS home agent runs in the foreground, listens on `127.0.0.1:3389` by default, keeps the relay dashboard presence socket connected, and opens an `.rdp` profile when `-open-rdp` is supplied. If your RDP app does not open automatically, connect it manually to:

```text
127.0.0.1:3389
```

### 7. Run Android Home App

Install the debug-signed APK:

```text
dist\android\deskferry-home-android-debug.apk
```

Open DeskFerry Home, enter the same relay room URL as the work agent, keep the local RDP port at `3389`, and start the tunnel. In an Android RDP client, connect to:

```text
127.0.0.1:3389
```

The Android app keeps the tunnel alive through a foreground service while you switch to the RDP client. It maintains the same `home-agent` presence socket used by the relay dashboard and a `dashboard` WebSocket for live relay status updates.

## Deliverables

### Azure Relay Web Service

`relay/azure-dotnet/` is a .NET 8 minimal ASP.NET Core service. It exposes:

- `GET /relay/` for the live overview dashboard.
- `GET /relay/{room}` for a room-scoped live dashboard.
- `GET /relay/health` for machine-readable health.
- `GET /relay/status` for JSON status.
- `GET /relay/ws` and `GET /relay/{room}/ws` as WebSocket endpoints.

WebSocket clients identify their role with:

```text
X-DeskFerry-Role: agent | client | home-agent | probe | dashboard
```

Relays also accept the former `X-TunnelDesktop-Role` header during the rename transition.

Roles:

- `agent`: work-side idle socket waiting to be paired.
- `client`: home-side data socket for one RDP connection.
- `home-agent`: Windows, macOS, or Android home-agent status presence.
- `probe`: self-test connection.
- `dashboard`: browser status stream.

The dashboard shows work-agent presence, home-app presence, active stream counts, total stream count, and recent remote addresses. It also serves the DeskFerry icon as `/relay/icon.svg` for favicon and header branding.

### Python Relay Web Service

`relay/python/` is a FastAPI/ASGI implementation of the same relay contract. It is useful for hosting on Python-capable App Service plans, on a Linux VM such as OCI Always Free, or for local relay testing without the .NET runtime.

It exposes the same user-facing paths:

- `GET /relay/` and `GET /relay/<room>` for the live dashboard.
- `GET /relay/health` for health JSON.
- `GET /relay/status` for JSON status.
- `GET /relay/ws` and `GET /relay/<room>/ws` as WebSocket endpoints.

Run it locally:

```powershell
python -m pip install -r relay\python\requirements.txt
python -m uvicorn app:app --app-dir relay\python --host 127.0.0.1 --port 8000
```

Build the deployable Python zip:

```powershell
python -m pip install -r relay\python\requirements-dev.txt
.\build\build-python-relay.ps1
```

The build emits both the source-style zip and an Oracle Linux 9 / Python 3.9 vendored zip for minimal VM deployments.

### Work Agent

`agent.exe` is the work-side Windows component. It is Windows-service-first because RDP must work while the user is logged out.

Default behavior:

- `agent.exe` with no args uses the default relay room URL.
- `-relay-url <url>` selects a named room.
- `-relay-url` can be repeated or can contain comma, semicolon, or newline separated URLs.
- The service keeps a small pool of idle outbound WebSockets per configured relay URL.
- When any configured relay pairs a socket, the agent dials `127.0.0.1:3389` and pipes bytes.

Debug and operations:

```powershell
.\agent.exe -console -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\agent.exe -console -relay-url "https://test-officialwebsite.azurewebsites.net/relay/workdesk;http://217.142.228.117/relay/workdesk"
.\agent.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
.\agent.exe -status
.\agent.exe -uninstall
```

### Agent Configurator

`deskferry-agent-configurator-windows-amd64.exe` is the native Windows setup and service management GUI.

It:

- Prefers `D:\DeskFerry\Agent` as the install directory when `D:` exists.
- Copies the selected agent binary to `agent.exe`.
- Installs or updates the automatic `DeskFerryAgent` Windows service with the configured relay URL list.
- Configures SCM restart recovery.
- Starts, stops, restarts, uninstalls, refreshes status, opens the install folder, and runs `agent.exe -self-test`.

### Windows Home App

`home-agent/windows/` is the secure home-side Windows path. It provides:

- A polished control panel with relay room URL, local RDP address, proxy mode, status tiles, room details, and activity log.
- A notification-area icon with open, connect, stop, Remote Desktop, and quit actions.
- Windows Credential Manager integration for saved RDP login credentials.
- Persistent home-app presence on the relay dashboard.
- A loopback RDP listener, normally `127.0.0.1:3390`.
- Automatic Remote Desktop launch when the user clicks `Connect`.

### macOS Home Agent

`home-agent/macos/` is the macOS home-side command-line agent. It provides:

- A foreground local RDP listener, normally `127.0.0.1:3389`.
- One outbound `client` WebSocket per local RDP connection.
- A persistent `home-agent` presence WebSocket while it runs.
- `-status` for relay room status.
- `-open-rdp` to write and open a local `.rdp` profile with the configured loopback target.

### Android Home App

`home-agent/android/` is the Android home endpoint. It is not an RDP client by itself; it provides the loopback tunnel that an Android RDP client uses.

It provides:

- A native Android control panel with relay room URL, local RDP port, status tiles, activity log, copy, dashboard, and RDP launch actions.
- A foreground service so the tunnel can keep running while another app is active.
- A loopback RDP listener, normally `127.0.0.1:3389`.
- One outbound `client` WebSocket per local RDP connection.
- A persistent `home-agent` presence WebSocket while the service is running.
- A persistent `dashboard` WebSocket for real-time work-agent and stream status.

Good free Android RDP client options include Microsoft's Remote Desktop/Windows App client and the open-source FreeRDP-based aFreeRDP client. Configure the RDP client to connect to the DeskFerry local target shown in the Android app.

## Security Model

- Work and home endpoints make outbound HTTPS WebSocket connections only.
- The room name in the URL is the pairing key.
- The relay never dials the work PC or home PC.
- The work agent only dials `127.0.0.1:3389` after Azure has paired a same-room home connection.
- Home apps listen on loopback by default, so other LAN devices cannot connect to the local RDP listener unless the user intentionally changes the listen address.

Choose room names that are not obvious. Anyone who knows the room URL can attempt to join that room.

This software can route around corporate egress controls to expose an internal RDP session. Confirm that use is permitted by workplace policy. This project intentionally does not add anti-monitoring, stealth, or obfuscation behavior.

## Build Prerequisites

Required:

- Go 1.25+.
- .NET SDK 8+ for the Azure relay.
- Python 3.11+ for the Python relay.
- JDK 17+ plus Android SDK platform 35 and build-tools 35.0.0 for the Android home app.
- Gradle 9.x, or a compatible Gradle installation on `PATH`, for the Android home app.
- `rsrc` for Windows GUI manifest resources; `build\build-go.ps1` installs it under `D:\Go\bin` when missing.

This repo has been built with Go installed under `D:\Scoop` and .NET SDK 9.x publishing the relay as `net8.0`.

## Build Commands

Build Azure relay zip:

```powershell
.\build\build-azure-relay.ps1
```

Build Python relay zip:

```powershell
.\build\build-python-relay.ps1
```

Build Go binaries:

```powershell
.\build\build-go.ps1
```

Build Android home APK:

```powershell
.\build\build-android-home.ps1
```

Artifacts:

```text
dist\azure-relay\deskferry-azure-relay.zip
dist\python-relay\deskferry-python-relay.zip
dist\python-relay\deskferry-python-relay-linux-cp39-vendored.zip
dist\bin\deskferry-agent-windows-amd64.exe
dist\bin\deskferry-agent-configurator-windows-amd64.exe
dist\bin\deskferry-home-windows-amd64.exe
dist\bin\deskferry-home-macos-arm64
dist\bin\deskferry-home-macos-amd64
dist\android\deskferry-home-android-debug.apk
```

## URL Configuration

Use one shared room name:

```text
https://test-officialwebsite.azurewebsites.net/relay/<room>
```

OCI Python relay example:

```text
http://217.142.228.117/relay/<room>
```

Rules:

- `<room>` is created automatically on first use.
- Reusing the same URL joins the same room.
- The work agent may use multiple relay URLs at once when each URL uses the same `<room>`.
- The home app chooses one relay URL for the current connection.
- The WebSocket endpoint is derived automatically as `/relay/<room>/ws`.
- The base `/relay/` path is an overview dashboard.
- No generated pairing files are required for the normal Azure WebSocket path.

## Troubleshooting

### Agent Self-Test Fails Through Proxy

Run:

```powershell
.\agent.exe -self-test -relay-url https://test-officialwebsite.azurewebsites.net/relay/workdesk
```

Self-test checks local RDP and then opens a `probe` WebSocket to each configured relay URL.

Common causes:

- Corporate proxy blocks `CONNECT test-officialwebsite.azurewebsites.net:443`.
- Proxy requires authentication not supported by the service account.
- Azure App Service WebSockets are disabled.
- The Azure relay has not been deployed or is not running.
- Local RDP is disabled or not listening on `127.0.0.1:3389`.

### Home App Connects But RDP Fails

Check:

- The home app status tiles show a room URL that is also configured on the work agent.
- The room dashboard shows waiting work-agent sockets.
- The agent service is running.
- Work PC allows RDP.
- The configured Windows account is allowed to log in remotely.
- The home app local listen port is not already in use. If `127.0.0.1:3389` fails on the home PC, use `127.0.0.1:3390`.

### Saved RDP Login

The Windows home app can save RDP credentials through Windows Credential Manager. Enter the work Windows username and password, then click `Save RDP Login`. DeskFerry calls `cmdkey.exe` for the local RDP target, writes a password-free `%APPDATA%\DeskFerry\home-client.rdp` launch profile, and clears the password field after saving. The password is not written to `%APPDATA%\DeskFerry\home-client.json` or the `.rdp` profile.

`Open Remote Desktop` and `Connect` launch MSTSC with that `.rdp` profile, so saved credentials are used automatically when Windows allows them. Remote Desktop may still prompt if Windows policy blocks saved credential delegation. In that case, allow saved credentials for the `TERMSRV/*` target through Windows policy, or continue signing in manually.

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

Rebuild after Go agent/client changes:

```powershell
.\build\build-go.ps1
```

## Repository Layout

```text
relay/azure-dotnet/      .NET Azure App Service WebSocket relay
relay/python/            Python/FastAPI WebSocket relay
work-agent/windows/service
                         Windows service work-side agent
work-agent/windows/configurator
                         Windows service setup/configurator GUI
home-agent/windows       Windows control-panel/tray home app
home-agent/macos         macOS foreground CLI home agent
home-agent/android       Android foreground-service home app
internal/tunnel          WebSocket, proxy, pipe, and role helpers
build/                   build scripts
```

## Status

This repo currently contains:

- Azure App Service WebSocket relay source and publish script.
- Protocol-compatible Python WebSocket relay source and publish script.
- Live dashboard with WebSocket status updates.
- Named-room URL joining under `/relay/<room>`.
- Windows work agent implemented as a Windows service deliverable.
- Windows configurator GUI for installing and managing the work agent service.
- Windows home app implemented as a friendly control-panel and tray deliverable.
- macOS home agent implemented as a foreground CLI tunnel endpoint.
- Android home app implemented as a foreground-service loopback tunnel endpoint.
- Build scripts for Go binaries, relay packages, and the Android APK.

## Current Limitations

- The Azure relay is a simple in-memory broker. Restarting the App Service disconnects active sessions and clears room status.
- Multiple App Service instances are not supported unless sticky routing or shared broker state is added.
- The Go work agent supports direct, environment, and basic HTTP proxy URLs. NTLM proxy authentication is not implemented in the service.
- The Windows home app is an RDP launcher and tunnel endpoint, not a full RDP client.
- The macOS home agent is a tunnel endpoint and `.rdp` launcher, not a full RDP client; use Microsoft Remote Desktop/Windows App or another macOS RDP client against `127.0.0.1:3389`.
- The Android home app is also a tunnel endpoint, not a full RDP client; use a separate Android RDP client against `127.0.0.1:3389`.

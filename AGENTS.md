# AGENTS.md

## Project Overview

TunnelDesktop is a Go + .NET project for an outbound-only RDP rendezvous tunnel. The normal relay is the Azure App Service at `https://test-officialwebsite.azurewebsites.net/relay/`, implemented by `azure-relay/`. The work-side Windows service and the home-side Windows app are configured by one shared relay room URL such as `https://test-officialwebsite.azurewebsites.net/relay/workdesk`.

## Architecture Rules

- The Azure App Service relay under `/relay/` is the normal broker.
- A named room URL under `/relay/<room>` is the normal and only user-facing pairing configuration.
- Do not reintroduce generated client files or file-based pairing artifacts for the normal path.
- The Azure relay WebSocket endpoint is `/relay/ws` for the overview room and `/relay/<room>/ws` for named rooms.
- URL-only WebSocket clients should use standard proxy environment variables by default; explicit `-proxy direct` is the bypass path.
- The home side is the Windows home app in `cmd/client`; do not reintroduce mobile home-agent apps.
- The Windows home app may keep a lightweight `home-agent` WebSocket open for dashboard presence, but RDP data uses `client` sockets.
- The Windows home app should remain control-panel/tray-first. Console mode is debug-only.
- The Windows home app should listen on loopback by default, normally `127.0.0.1:3389`.
- `cmd/agent` must remain Windows-service-first. Console mode is debug-only.
- `cmd/agent-configurator` owns the native Windows setup/configurator GUI for the work agent service.
- Do not add stealth, anti-monitoring, or obfuscation behavior.

## Sensitive Files

Relay configs with inline PEM, generated cert directories for local relay experiments, logs, and screenshots can contain private material. Do not commit generated certs, generated local relay configs, logs containing secrets, or screenshots that expose sensitive values.

The current `.gitignore` excludes `dist/`, `bin/`, `obj/`, generated executables, and generated resource objects. Preserve that behavior.

## Build Commands

Run Go tests:

```powershell
go test ./...
```

Build Azure relay zip:

```powershell
.\build\build-azure-relay.ps1
```

Build Windows/Linux Go artifacts:

```powershell
.\build\build-go.ps1
```

When `cmd/agent`, `cmd/agent-configurator`, `cmd/client`, `internal/tunnel`, or shared relay behavior changes, run `go test ./...` and `.\build\build-go.ps1`. When `azure-relay/` changes, run `.\build\build-azure-relay.ps1`.

## Tooling Notes

This repo has been built with:

- Go 1.26.x installed under `D:\Scoop`
- .NET SDK 9.x, publishing the relay as `net8.0`
- rsrc under `D:\Go\bin` for Windows GUI manifest resources

## Deployment

Deploy the Azure relay to the App Service backing `https://test-officialwebsite.azurewebsites.net/relay/`.

Deployable artifacts:

- `dist/azure-relay/tunneldesktop-azure-relay.zip`
- `dist/bin/agent-windows-amd64.exe`
- `dist/bin/agent-configurator-windows-amd64.exe`
- `dist/bin/client-windows-amd64.exe`

After code changes, build the relevant artifacts. The user prefers deployment after code changes. Use the authenticated browser/Kudu path for the Azure App Service when requested.

## Verification Expectations

For documentation-only changes, read the modified files back or run a lightweight command if useful.

For Go changes, run:

```powershell
go test ./...
```

For release-impacting Go changes, also run:

```powershell
.\build\build-go.ps1
```

For Azure relay changes, run:

```powershell
.\build\build-azure-relay.ps1
```

## Coding Guidance

- Prefer existing package boundaries and helper APIs.
- Keep `internal/tunnel` focused on protocol primitives: TLS, auth, proxy CONNECT, WebSocket dialing, yamux config, byte piping, and allowlist.
- Keep the Windows home app URL-first; it should store a room URL and local UI settings, not implement the broker.
- Use `apply_patch` for manual file edits.
- Avoid committing generated private key material under `dist/` or elsewhere.

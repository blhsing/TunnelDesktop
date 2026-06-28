# DeskFerry Go Relay

This is the lightweight Go implementation of the DeskFerry WebSocket relay. It is protocol-compatible with the Azure .NET relay and the Python/FastAPI relay.

It exposes:

- `GET /relay/` and `GET /relay/<room>` dashboard UI.
- `GET /relay/health` health JSON.
- `GET /relay/status?room=<room>` status JSON.
- `GET /relay/icon.svg` favicon and branding SVG.
- `GET /relay/ws` and `GET /relay/<room>/ws` WebSocket endpoints.

WebSocket clients identify their role with `X-DeskFerry-Role`:

```text
agent | client | home-agent | probe | dashboard
```

## Build

Run from the repository root:

```powershell
.\build\build-go.ps1
```

The OCI deployable binary is:

```text
dist\bin\deskferry-relay-linux-amd64
```

## OCI Deployment

The active OCI Always Free VM relay runs this Go binary at:

```text
http://217.142.228.117/relay/b
```

Service layout:

```text
/opt/deskferry/go-relay/deskferry-relay
/etc/systemd/system/deskferry-relay.service
```

The systemd service uses:

```text
GOMEMLIMIT=192MiB
GOGC=75
/opt/deskferry/go-relay/deskferry-relay -listen 0.0.0.0:80
```

Useful checks on the VM:

```sh
systemctl status deskferry-relay
systemctl status deskferry-relay-healthcheck.timer
ps -o pid,rss,comm,args -p "$(systemctl show -p MainPID --value deskferry-relay.service)"
curl -fsS http://127.0.0.1/relay/health
curl -fsS 'http://127.0.0.1/relay/status?room=b'
```

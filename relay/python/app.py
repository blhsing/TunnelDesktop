from __future__ import annotations

import asyncio
import json
import logging
import uuid
from collections import deque
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from fastapi import FastAPI, WebSocket
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse, Response
from starlette.websockets import WebSocketDisconnect, WebSocketState

SERVICE_NAME = "DeskFerry.Relay"
DASHBOARD_ROLE = "dashboard"
VALID_ROLES = {"agent", "client", "home-agent", "probe", DASHBOARD_ROLE}

logger = logging.getLogger("deskferry.relay")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

app = FastAPI(title="DeskFerry Python Relay", docs_url=None, redoc_url=None)


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


def json_time(value: datetime | None) -> str | None:
    if value is None:
        return None
    return value.isoformat().replace("+00:00", "Z")


def room_id(token: str | None) -> str:
    raw = (token or "").strip().strip("/")
    if not raw:
        return "default"

    out: list[str] = []
    for ch in raw:
        if len(out) >= 64:
            break
        lower = ch.lower()
        if "a" <= lower <= "z" or "0" <= lower <= "9" or lower in "-_.":
            out.append(lower)
        elif not out or out[-1] != "-":
            out.append("-")

    normalized = "".join(out).strip("-.")
    return normalized or "default"


def websocket_is_connected(websocket: WebSocket) -> bool:
    return (
        websocket.client_state == WebSocketState.CONNECTED
        and websocket.application_state == WebSocketState.CONNECTED
    )


def websocket_remote(websocket: WebSocket) -> str:
    forwarded = websocket.headers.get("x-forwarded-for", "")
    if forwarded.strip():
        return forwarded.split(",", 1)[0].strip()
    return websocket.client.host if websocket.client else "unknown"


def read_role(websocket: WebSocket) -> str | None:
    value = (
        websocket.headers.get("x-deskferry-role")
        or websocket.headers.get("x-tunneldesktop-role")
        or websocket.query_params.get("role")
    )
    role = (value or "").strip().lower()
    return role if role in VALID_ROLES else None


def read_token(websocket: WebSocket) -> str | None:
    auth = websocket.headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        token = auth[7:].strip()
        if token:
            return token

    token = (websocket.query_params.get("token") or "").strip()
    if token:
        return token

    room = (websocket.query_params.get("room") or "").strip()
    return room or "default"


async def close_quietly(websocket: WebSocket, code: int = 1000, reason: str = "") -> None:
    try:
        if websocket.application_state == WebSocketState.CONNECTED:
            await websocket.close(code=code, reason=reason)
    except Exception:
        pass


async def drain_until_close(websocket: WebSocket) -> None:
    try:
        while websocket_is_connected(websocket):
            message = await websocket.receive()
            if message["type"] == "websocket.disconnect":
                return
    except WebSocketDisconnect:
        return


async def pump_binary(source: WebSocket, destination: WebSocket) -> None:
    while websocket_is_connected(source) and websocket_is_connected(destination):
        message = await source.receive()
        if message["type"] == "websocket.disconnect":
            return
        payload = message.get("bytes")
        if payload is not None:
            await destination.send_bytes(payload)


@dataclass
class HomePeer:
    websocket: WebSocket
    remote: str
    done: asyncio.Future[None]


@dataclass
class WaitingAgent:
    websocket: WebSocket
    remote: str
    paired: asyncio.Future[HomePeer] = field(default_factory=asyncio.Future)

    @property
    def is_open(self) -> bool:
        return websocket_is_connected(self.websocket) and not self.paired.done()

    def try_pair(self, peer: HomePeer) -> bool:
        if self.paired.done():
            return False
        self.paired.set_result(peer)
        return True

    def try_cancel(self) -> None:
        if not self.paired.done():
            self.paired.cancel()


class RelayRoom:
    def __init__(self, room: str) -> None:
        self.id = room
        self._lock = asyncio.Lock()
        self._agents: deque[WaitingAgent] = deque()
        self._active_pairs = 0
        self._total_pairs = 0
        self._last_agent_remote: str | None = None
        self._last_agent_connected_at: datetime | None = None
        self._last_agent_disconnected_at: datetime | None = None
        self._home_agent_remote: str | None = None
        self._home_agent_connected_at: datetime | None = None
        self._home_agent_disconnected_at: datetime | None = None
        self._last_client_remote: str | None = None
        self._last_client_connected_at: datetime | None = None
        self._last_client_disconnected_at: datetime | None = None

    async def enqueue_agent(self, websocket: WebSocket, remote: str) -> WaitingAgent:
        waiting = WaitingAgent(websocket, remote)
        async with self._lock:
            self._prune_closed_agents_locked()
            self._agents.append(waiting)
            self._last_agent_remote = remote
            self._last_agent_connected_at = utc_now()
        return waiting

    async def try_take_agent(self) -> WaitingAgent | None:
        async with self._lock:
            self._prune_closed_agents_locked()
            while self._agents:
                waiting = self._agents.popleft()
                if waiting.is_open:
                    return waiting
        return None

    async def remove_waiting(self, waiting: WaitingAgent) -> None:
        async with self._lock:
            self._agents = deque(agent for agent in self._agents if agent is not waiting)
            self._last_agent_disconnected_at = utc_now()

    async def home_agent_connected(self, remote: str) -> None:
        async with self._lock:
            self._home_agent_remote = remote
            self._home_agent_connected_at = utc_now()

    async def home_agent_disconnected(self, remote: str) -> None:
        async with self._lock:
            if self._home_agent_remote == remote:
                self._home_agent_remote = None
                self._home_agent_connected_at = None
                self._home_agent_disconnected_at = utc_now()

    async def bridge(
        self,
        agent: WebSocket,
        client: WebSocket,
        agent_remote: str,
        client_remote: str,
        client_done: asyncio.Future[None],
        state_changed: Any,
    ) -> None:
        async with self._lock:
            self._active_pairs += 1
            self._total_pairs += 1
            self._last_client_remote = client_remote
            self._last_client_connected_at = utc_now()
            self._last_client_disconnected_at = None
        state_changed()

        left = asyncio.create_task(pump_binary(agent, client))
        right = asyncio.create_task(pump_binary(client, agent))
        try:
            done, pending = await asyncio.wait({left, right}, return_when=asyncio.FIRST_COMPLETED)
            for task in pending:
                task.cancel()
            await asyncio.gather(*done, *pending, return_exceptions=True)
        finally:
            async with self._lock:
                self._active_pairs = max(0, self._active_pairs - 1)
                self._last_agent_disconnected_at = utc_now()
                self._last_client_disconnected_at = utc_now()
            await close_quietly(agent)
            await close_quietly(client)
            if not client_done.done():
                client_done.set_result(None)
            state_changed()
            logger.info("bridge closed room=%s agent=%s client=%s", self.id, agent_remote, client_remote)

    async def snapshot(self) -> dict[str, Any]:
        async with self._lock:
            self._prune_closed_agents_locked()
            return {
                "id": self.id,
                "waiting_agents": len(self._agents),
                "active_pairs": self._active_pairs,
                "total_pairs": self._total_pairs,
                "last_agent_remote": self._last_agent_remote,
                "last_agent_connected_at": json_time(self._last_agent_connected_at),
                "last_agent_disconnected_at": json_time(self._last_agent_disconnected_at),
                "home_agent_connected": self._home_agent_remote is not None,
                "home_agent_remote": self._home_agent_remote,
                "home_agent_connected_at": json_time(self._home_agent_connected_at),
                "home_agent_disconnected_at": json_time(self._home_agent_disconnected_at),
                "last_client_remote": self._last_client_remote,
                "last_client_connected_at": json_time(self._last_client_connected_at),
                "last_client_disconnected_at": json_time(self._last_client_disconnected_at),
            }

    def _prune_closed_agents_locked(self) -> None:
        self._agents = deque(agent for agent in self._agents if agent.is_open)


@dataclass
class DashboardClient:
    id: str
    websocket: WebSocket
    room: str | None
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)


class RelayHub:
    def __init__(self) -> None:
        self._lock = asyncio.Lock()
        self._rooms: dict[str, RelayRoom] = {}
        self._dashboards: dict[str, DashboardClient] = {}

    async def serve_agent(self, token: str, websocket: WebSocket, remote: str) -> None:
        room = await self._room_for(token)
        waiting = await room.enqueue_agent(websocket, remote)
        logger.info("agent waiting room=%s remote=%s", room.id, remote)
        self.notify_dashboards()

        try:
            peer = await waiting.paired
            logger.info("pairing room=%s agent=%s client=%s", room.id, remote, peer.remote)
            await websocket.send_text("start")
            await peer.websocket.send_text("start")
            await room.bridge(websocket, peer.websocket, remote, peer.remote, peer.done, self.notify_dashboards)
        except (asyncio.CancelledError, WebSocketDisconnect):
            pass
        finally:
            waiting.try_cancel()
            await room.remove_waiting(waiting)
            self.notify_dashboards()

    async def serve_client(self, token: str, websocket: WebSocket, remote: str) -> None:
        room = await self._room_for(token)
        waiting = await room.try_take_agent()
        if waiting is None:
            logger.info("client rejected without agent room=%s remote=%s", room.id, remote)
            await close_quietly(websocket, 1013, "no work agent connected")
            return

        done: asyncio.Future[None] = asyncio.Future()
        if not waiting.try_pair(HomePeer(websocket, remote, done)):
            await close_quietly(websocket, 1013, "work agent unavailable")
            return
        self.notify_dashboards()

        try:
            await done
        except asyncio.CancelledError:
            pass

    async def serve_home_agent(self, token: str, websocket: WebSocket, remote: str) -> None:
        room = await self._room_for(token)
        await room.home_agent_connected(remote)
        logger.info("home app connected room=%s remote=%s", room.id, remote)
        self.notify_dashboards()
        try:
            await drain_until_close(websocket)
        finally:
            await room.home_agent_disconnected(remote)
            self.notify_dashboards()
            logger.info("home app disconnected room=%s remote=%s", room.id, remote)

    async def serve_dashboard(self, websocket: WebSocket, remote: str, room: str | None) -> None:
        client = DashboardClient(str(uuid.uuid4()), websocket, room_id(room) if room else None)
        self._dashboards[client.id] = client
        logger.info("dashboard connected remote=%s", remote)
        try:
            await self._send_dashboard(client)
            await drain_until_close(websocket)
        finally:
            self._dashboards.pop(client.id, None)
            await close_quietly(websocket)
            logger.info("dashboard disconnected remote=%s", remote)

    async def snapshot(self, room: str | None = None) -> dict[str, Any]:
        selected = room_id(room) if room else None
        async with self._lock:
            rooms = list(self._rooms.values())

        if selected is not None:
            rooms = [candidate for candidate in rooms if candidate.id == selected]

        return {
            "service": SERVICE_NAME,
            "time": json_time(utc_now()),
            "rooms": [await candidate.snapshot() for candidate in sorted(rooms, key=lambda item: item.id)],
        }

    def notify_dashboards(self) -> None:
        for client in list(self._dashboards.values()):
            asyncio.create_task(self._send_dashboard(client))

    async def _room_for(self, token: str) -> RelayRoom:
        key = room_id(token)
        async with self._lock:
            if key not in self._rooms:
                self._rooms[key] = RelayRoom(key)
            return self._rooms[key]

    async def _send_dashboard(self, client: DashboardClient) -> None:
        if not websocket_is_connected(client.websocket):
            self._dashboards.pop(client.id, None)
            return

        try:
            async with client.lock:
                if not websocket_is_connected(client.websocket):
                    self._dashboards.pop(client.id, None)
                    return
                payload = json.dumps(await self.snapshot(client.room), separators=(",", ":"))
                await asyncio.wait_for(client.websocket.send_text(payload), timeout=10)
        except Exception:
            self._dashboards.pop(client.id, None)


hub = RelayHub()


@app.get("/", include_in_schema=False)
async def root() -> RedirectResponse:
    return RedirectResponse("/relay/")


@app.get("/relay", response_class=HTMLResponse, include_in_schema=False)
@app.get("/relay/", response_class=HTMLResponse, include_in_schema=False)
async def dashboard() -> HTMLResponse:
    return HTMLResponse(dashboard_html(""))


@app.get("/relay/health")
async def health() -> JSONResponse:
    return JSONResponse({"status": "ok", "service": SERVICE_NAME, "time": json_time(utc_now())})


@app.get("/relay/icon.svg", include_in_schema=False)
async def icon() -> Response:
    return Response(icon_svg(), media_type="image/svg+xml")


@app.get("/relay/status")
async def status(room: str | None = None) -> JSONResponse:
    return JSONResponse(await hub.snapshot(room))


@app.get("/relay/{room}", response_class=HTMLResponse, include_in_schema=False)
async def room_dashboard(room: str) -> HTMLResponse:
    return HTMLResponse(dashboard_html(room))


@app.websocket("/relay/ws")
async def relay_websocket_default(websocket: WebSocket) -> None:
    await relay_websocket(websocket, None)


@app.websocket("/relay/{room}/ws")
async def relay_websocket_room(websocket: WebSocket, room: str) -> None:
    await relay_websocket(websocket, room)


async def relay_websocket(websocket: WebSocket, room: str | None) -> None:
    role = read_role(websocket)
    token = room or (DASHBOARD_ROLE if role == DASHBOARD_ROLE else read_token(websocket))
    if role is None or token is None:
        await websocket.accept()
        await close_quietly(websocket, 1008, "missing relay role or bearer token")
        return

    await websocket.accept()
    await asyncio.sleep(0)
    remote = websocket_remote(websocket)
    if role == DASHBOARD_ROLE:
        await hub.serve_dashboard(websocket, remote, room)
    elif role == "agent":
        await hub.serve_agent(token, websocket, remote)
    elif role == "client":
        await hub.serve_client(token, websocket, remote)
    elif role == "home-agent":
        await hub.serve_home_agent(token, websocket, remote)
    elif role == "probe":
        await close_quietly(websocket, 1000, "probe ok")
    else:
        await close_quietly(websocket, 1008, "unsupported role")


def icon_svg() -> str:
    return """<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 108 108">
  <defs>
    <linearGradient id="bg" x1="12" y1="12" x2="96" y2="96" gradientUnits="userSpaceOnUse">
      <stop stop-color="#13324d"/>
      <stop offset="1" stop-color="#40b5ae"/>
    </linearGradient>
    <clipPath id="clip">
      <rect x="6" y="6" width="96" height="96" rx="22"/>
    </clipPath>
  </defs>
  <rect x="6" y="6" width="96" height="96" rx="22" fill="url(#bg)"/>
  <g clip-path="url(#clip)">
    <path d="M6 34c22-17 61-14 97-24l3 12c-32 12-70 9-99 23z" fill="#fff" opacity=".08"/>
  </g>
  <path d="M12 35c19-13 38-6 56-18M43 97c16-13 37-6 60-19" fill="none" stroke="#fff" stroke-width="1.2" stroke-linecap="round" opacity=".22"/>
  <path d="M70 31c12-8 22-4 33-12" fill="none" stroke="#fff" stroke-width=".7" stroke-linecap="round" opacity=".18"/>
  <path d="M27 28q0-7 7-7h40q7 0 7 7v28q0 7-7 7H34q-7 0-7-7z" fill="#031727" opacity=".22"/>
  <path d="M27 25q0-7 7-7h40q7 0 7 7v28q0 7-7 7H34q-7 0-7-7z" fill="#fff"/>
  <path d="M34 27q0-3 3-3h34q3 0 3 3v20q0 3-3 3H37q-3 0-3-3z" fill="#17324d"/>
  <path d="M38 27h12l-9 23h-7z" fill="#fff" opacity=".14"/>
  <path d="M40 29h26" fill="none" stroke="#fff" stroke-width=".65" stroke-linecap="round" opacity=".20"/>
  <path d="M49 59h10l3 8H46zM39 68q0-3 3-3h24q3 0 3 3v3H39z" fill="#fff"/>
  <path d="M20 67h68l-8 11q-9 7-42 4q-9-2-18-15z" fill="#031727" opacity=".20"/>
  <path d="M20 64h68l-8 11q-9 7-42 4q-9-2-18-15z" fill="#e66d4f"/>
  <path d="M38 77c12 4 28 3 42-2" fill="none" stroke="#71323a" stroke-width=".8" stroke-linecap="round" opacity=".28"/>
  <path d="M31 66h43q2 0 2 2t-2 2H31q-2 0-2-2t2-2z" fill="#fff" opacity=".76"/>
  <g clip-path="url(#clip)">
    <path d="M0 78q13-7 27 0t28 0t28 0q13 7 25-2v32H0z" fill="#69d2c7"/>
    <path d="M4 86q18-7 36 0t36 0q16-6 28-2v4q-13-2-28 3q-18 7-36 0q-18-7-36 0z" fill="#fff" opacity=".48"/>
    <path d="M17 92c8-3 15-2 22 0M73 96c7-3 15-2 21-5" fill="none" stroke="#fff" stroke-width=".65" stroke-linecap="round" opacity=".36"/>
    <path d="M14 97c20-5 31 3 52-2" fill="none" stroke="#fff" stroke-width=".8" stroke-linecap="round" opacity=".32"/>
  </g>
</svg>"""


def dashboard_html(room: str) -> str:
    room_json = json.dumps(room)
    return f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DeskFerry Relay</title>
  <link rel="icon" href="/relay/icon.svg" type="image/svg+xml">
  <style>
    :root {{
      color-scheme: light;
      --bg: #f5f7f8;
      --panel: #ffffff;
      --ink: #1f2933;
      --muted: #65717d;
      --line: #d7dee3;
      --accent: #2f6f73;
      --ok: #287d52;
      --warn: #9a6a12;
      --bad: #a94343;
    }}
    * {{ box-sizing: border-box; }}
    body {{
      margin: 0;
      font-family: "Segoe UI", system-ui, -apple-system, BlinkMacSystemFont, sans-serif;
      background: var(--bg);
      color: var(--ink);
    }}
    header {{
      padding: 28px 24px 18px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }}
    main {{
      width: min(1120px, calc(100% - 32px));
      margin: 22px auto 40px;
    }}
    h1 {{
      margin: 0 0 6px;
      font-size: clamp(26px, 4vw, 38px);
      letter-spacing: 0;
    }}
    .brand {{
      display: flex;
      align-items: center;
      gap: 14px;
    }}
    .brand-icon {{
      width: 58px;
      height: 58px;
      flex: 0 0 58px;
      border-radius: 13px;
    }}
    .brand-text {{ min-width: 0; }}
    .subtle {{ color: var(--muted); }}
    .toolbar {{
      display: flex;
      gap: 10px;
      align-items: center;
      flex-wrap: wrap;
      margin-top: 16px;
    }}
    .toolbar input {{
      flex: 1 1 360px;
      min-width: 0;
      height: 40px;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 0 12px;
      color: var(--ink);
      background: #fbfcfd;
      font: 13px ui-monospace, SFMono-Regular, Consolas, monospace;
    }}
    .toolbar button {{
      height: 40px;
      border: 1px solid var(--accent);
      border-radius: 8px;
      padding: 0 14px;
      color: var(--accent);
      background: #fff;
      font-weight: 700;
      cursor: pointer;
    }}
    .grid {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 14px;
      margin-bottom: 18px;
    }}
    .card {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 16px;
      min-height: 128px;
    }}
    .label {{
      color: var(--muted);
      font-size: 13px;
      font-weight: 700;
      text-transform: uppercase;
    }}
    .value {{
      margin-top: 10px;
      font-size: 28px;
      font-weight: 700;
      line-height: 1.1;
    }}
    .ok {{ color: var(--ok); }}
    .warn {{ color: var(--warn); }}
    .bad {{ color: var(--bad); }}
    table {{
      width: 100%;
      border-collapse: collapse;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
    }}
    th, td {{
      padding: 12px 14px;
      text-align: left;
      border-bottom: 1px solid var(--line);
      vertical-align: top;
      font-size: 14px;
    }}
    th {{
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      background: #fbfcfd;
    }}
    tr:last-child td {{ border-bottom: 0; }}
    code {{
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 13px;
    }}
    .pill {{
      display: inline-block;
      padding: 3px 8px;
      border-radius: 999px;
      border: 1px solid var(--line);
      font-size: 12px;
      font-weight: 700;
      background: #f9fafb;
    }}
    .pill.ok {{ border-color: #bfe4cf; background: #edf8f1; }}
    .pill.bad {{ border-color: #efc5c5; background: #fff0f0; }}
    @media (max-width: 760px) {{
      .grid {{ grid-template-columns: 1fr; }}
      th:nth-child(5), td:nth-child(5) {{ display: none; }}
      .brand-icon {{
        width: 48px;
        height: 48px;
        flex-basis: 48px;
      }}
    }}
  </style>
</head>
<body>
  <header>
    <div class="brand">
      <img class="brand-icon" src="/relay/icon.svg" alt="">
      <div class="brand-text">
        <h1>DeskFerry Relay</h1>
        <div class="subtle">Python WebSocket relay at <code>/relay/ws</code>. Status updates stream live over WebSocket.</div>
      </div>
    </div>
    <div class="toolbar">
      <input id="roomUrl" readonly aria-label="Relay room URL">
      <button id="copyRoom" type="button">Copy</button>
    </div>
  </header>
  <main>
    <section class="grid">
      <div class="card">
        <div class="label">Work agent</div>
        <div id="workStatus" class="value warn">Checking</div>
        <p id="workDetail" class="subtle">Waiting for status.</p>
      </div>
      <div class="card">
        <div class="label">Home app</div>
        <div id="homeStatus" class="value warn">Checking</div>
        <p id="homeDetail" class="subtle">Waiting for status.</p>
      </div>
      <div class="card">
        <div class="label">RDP streams</div>
        <div id="streamStatus" class="value">0</div>
        <p id="streamDetail" class="subtle">No active pairs.</p>
      </div>
    </section>
    <table>
      <thead>
        <tr>
          <th>Room</th>
          <th>Work Agent</th>
          <th>Home App</th>
          <th>Active Pairs</th>
          <th>Last Client</th>
        </tr>
      </thead>
      <tbody id="rooms">
        <tr><td colspan="5" class="subtle">Loading relay status...</td></tr>
      </tbody>
    </table>
  </main>
  <script>
    const roomsBody = document.getElementById("rooms");
    const workStatus = document.getElementById("workStatus");
    const workDetail = document.getElementById("workDetail");
    const homeStatus = document.getElementById("homeStatus");
    const homeDetail = document.getElementById("homeDetail");
    const streamStatus = document.getElementById("streamStatus");
    const streamDetail = document.getElementById("streamDetail");
    const roomUrl = document.getElementById("roomUrl");
    const copyRoom = document.getElementById("copyRoom");
    const pageRoom = {room_json};

    function pill(ok, text) {{
      return `<span class="pill ${{ok ? "ok" : "bad"}}">${{text}}</span>`;
    }}

    function esc(value) {{
      return String(value ?? "").replace(/[&<>"']/g, char => ({{
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;"
      }}[char]));
    }}

    function fmt(value) {{
      if (!value) return "";
      return new Date(value).toLocaleString();
    }}

    function setValue(node, text, cls) {{
      node.className = "value " + cls;
      node.textContent = text;
    }}

    function relayRoomUrl(room) {{
      if (!room) return `${{location.origin}}/relay/`;
      return `${{location.origin}}/relay/${{encodeURIComponent(room)}}`;
    }}

    function render(data) {{
      const rooms = data.rooms || [];
      const waitingAgents = rooms.reduce((sum, r) => sum + (r.waiting_agents || 0), 0);
      const activePairs = rooms.reduce((sum, r) => sum + (r.active_pairs || 0), 0);
      const homeAgents = rooms.filter(r => r.home_agent_connected).length;
      setValue(workStatus, waitingAgents + activePairs > 0 ? "Connected" : "Waiting", waitingAgents + activePairs > 0 ? "ok" : "warn");
      workDetail.textContent = `${{waitingAgents}} idle work sockets, ${{activePairs}} paired streams.`;
      setValue(homeStatus, homeAgents > 0 ? "Connected" : "Waiting", homeAgents > 0 ? "ok" : "warn");
      homeDetail.textContent = `${{homeAgents}} home app status connection${{homeAgents === 1 ? "" : "s"}}.`;
      streamStatus.textContent = activePairs.toString();
      streamDetail.textContent = activePairs === 0 ? "No active RDP streams." : `${{activePairs}} RDP stream${{activePairs === 1 ? "" : "s"}} bridged.`;
      if (rooms.length === 0) {{
        roomsBody.innerHTML = '<tr><td colspan="5" class="subtle">No rooms have connected yet.</td></tr>';
        return;
      }}
      roomsBody.innerHTML = rooms.map(r => {{
        const workConnected = (r.waiting_agents || 0) + (r.active_pairs || 0) > 0;
        return `<tr>
          <td><code>${{esc(r.id)}}</code></td>
          <td>${{pill(workConnected, workConnected ? "connected" : "waiting")}}<br><span class="subtle">${{r.waiting_agents || 0}} idle<br>${{esc(fmt(r.last_agent_connected_at))}}</span></td>
          <td>${{pill(!!r.home_agent_connected, r.home_agent_connected ? "connected" : "waiting")}}<br><span class="subtle">${{esc(r.home_agent_remote || "")}}<br>${{esc(fmt(r.home_agent_connected_at))}}</span></td>
          <td>${{r.active_pairs || 0}}<br><span class="subtle">${{r.total_pairs || 0}} total</span></td>
          <td><span class="subtle">${{esc(r.last_client_remote || "")}}<br>${{esc(fmt(r.last_client_connected_at))}}</span></td>
        </tr>`;
      }}).join("");
    }}

    function connectDashboard() {{
      const scheme = location.protocol === "https:" ? "wss:" : "ws:";
      const roomPath = pageRoom ? `/relay/${{encodeURIComponent(pageRoom)}}/ws` : "/relay/ws";
      const socket = new WebSocket(`${{scheme}}//${{location.host}}${{roomPath}}?role=dashboard`);
      socket.onmessage = event => render(JSON.parse(event.data));
      socket.onclose = () => {{
        setValue(workStatus, "Reconnecting", "warn");
        setValue(homeStatus, "Reconnecting", "warn");
        setTimeout(connectDashboard, 1500);
      }};
      socket.onerror = () => socket.close();
    }}

    roomUrl.value = relayRoomUrl(pageRoom);
    copyRoom.addEventListener("click", async () => {{
      roomUrl.select();
      await navigator.clipboard.writeText(roomUrl.value);
    }});
    connectDashboard();
  </script>
</body>
</html>"""

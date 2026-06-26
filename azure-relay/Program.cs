using System.Buffers;
using System.Collections.Concurrent;
using System.Net.WebSockets;
using System.Text;

var builder = WebApplication.CreateBuilder(args);
builder.Services.AddSingleton<RelayHub>();

var app = builder.Build();
app.UseWebSockets(new WebSocketOptions
{
    KeepAliveInterval = TimeSpan.FromSeconds(20)
});

app.MapGet("/", () => Results.Redirect("/relay/"));
app.MapGet("/relay", () => Results.Text(DashboardHtml(), "text/html; charset=utf-8"));
app.MapGet("/relay/health", () => Results.Json(new
{
    status = "ok",
    service = "TunnelDesktop.Relay",
    time = DateTimeOffset.UtcNow
}));
app.MapGet("/relay/status", (HttpContext context, RelayHub hub) => Results.Json(hub.Snapshot(context.Request.Query["room"].FirstOrDefault())));
app.MapGet("/relay/{room}", (string room) => Results.Text(DashboardHtml(room), "text/html; charset=utf-8"));

app.Map("/relay/ws", RelayWebSocketHandler);
app.Map("/relay/{room}/ws", RelayWebSocketHandler);

async Task RelayWebSocketHandler(HttpContext context, RelayHub hub)
{
    if (!context.WebSockets.IsWebSocketRequest)
    {
        context.Response.StatusCode = StatusCodes.Status426UpgradeRequired;
        await context.Response.WriteAsync("WebSocket upgrade required.");
        return;
    }

    var role = ReadRole(context.Request);
    var token = ReadRoom(context) ?? (role == "dashboard" ? "dashboard" : ReadToken(context.Request));
    if (role is null || token is null)
    {
        context.Response.StatusCode = StatusCodes.Status401Unauthorized;
        await context.Response.WriteAsync("Missing relay role or bearer token.");
        return;
    }

    using var socket = await context.WebSockets.AcceptWebSocketAsync();
    var remote = RemoteAddress(context);
    switch (role)
    {
        case "dashboard":
            await hub.ServeDashboardAsync(socket, remote, ReadRoom(context), context.RequestAborted);
            break;
        case "agent":
            await hub.ServeAgentAsync(token, socket, remote, context.RequestAborted);
            break;
        case "client":
            await hub.ServeClientAsync(token, socket, remote, context.RequestAborted);
            break;
        case "home-agent":
            await hub.ServeHomeAgentAsync(token, socket, remote, ReadHomeAgentInfo(context.Request), context.RequestAborted);
            break;
        case "probe":
            await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "probe ok", CancellationToken.None);
            break;
        default:
            await socket.CloseAsync(WebSocketCloseStatus.PolicyViolation, "unsupported role", CancellationToken.None);
            break;
    }
}

app.Run();

static string? ReadRole(HttpRequest request)
{
    var role = request.Headers["X-TunnelDesktop-Role"].FirstOrDefault() ?? request.Query["role"].FirstOrDefault();
    role = role?.Trim().ToLowerInvariant();
    return role is "agent" or "client" or "home-agent" or "probe" or "dashboard" ? role : null;
}

static string? ReadToken(HttpRequest request)
{
    var auth = request.Headers.Authorization.FirstOrDefault();
    if (!string.IsNullOrWhiteSpace(auth) && auth.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
    {
        var token = auth["Bearer ".Length..].Trim();
        return string.IsNullOrWhiteSpace(token) ? null : token;
    }
    var queryToken = request.Query["token"].FirstOrDefault()?.Trim();
    if (queryToken is { Length: > 0 })
    {
        return queryToken;
    }
    var room = request.Query["room"].FirstOrDefault()?.Trim();
    return string.IsNullOrWhiteSpace(room) ? "default" : room;
}

static string? ReadRoom(HttpContext context)
{
    var value = context.Request.RouteValues["room"]?.ToString()?.Trim();
    return string.IsNullOrWhiteSpace(value) ? null : value;
}

static string RemoteAddress(HttpContext context)
{
    var forwarded = context.Request.Headers["X-Forwarded-For"].FirstOrDefault();
    if (!string.IsNullOrWhiteSpace(forwarded))
    {
        return forwarded.Split(',')[0].Trim();
    }
    return context.Connection.RemoteIpAddress?.ToString() ?? "unknown";
}

static HomeAgentInfo ReadHomeAgentInfo(HttpRequest request)
{
    var hotspotIp = ReadHeaderValue(request, "X-TunnelDesktop-Hotspot-IP", 64);
    var privateIps = ReadHeaderList(request, "X-TunnelDesktop-Private-IPs", 12, 96);
    return new HomeAgentInfo(hotspotIp, privateIps);
}

static string? ReadHeaderValue(HttpRequest request, string name, int maxLength)
{
    var value = request.Headers[name].FirstOrDefault()?.Trim();
    if (string.IsNullOrWhiteSpace(value))
    {
        return null;
    }
    value = value.Replace("\r", "").Replace("\n", "");
    return value.Length > maxLength ? value[..maxLength] : value;
}

static string[] ReadHeaderList(HttpRequest request, string name, int maxItems, int maxItemLength)
{
    var value = ReadHeaderValue(request, name, maxItems * maxItemLength);
    if (string.IsNullOrWhiteSpace(value))
    {
        return [];
    }
    return value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
        .Select(item => item.Length > maxItemLength ? item[..maxItemLength] : item)
        .Where(item => item.Length > 0)
        .Take(maxItems)
        .ToArray();
}

static string DashboardHtml(string room = "")
{
    var roomJson = System.Text.Json.JsonSerializer.Serialize(room);
    return $$"""
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TunnelDesktop Relay</title>
  <style>
    :root {
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
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Segoe UI", system-ui, -apple-system, BlinkMacSystemFont, sans-serif;
      background: var(--bg);
      color: var(--ink);
    }
    header {
      padding: 28px 24px 18px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }
    main {
      width: min(1120px, calc(100% - 32px));
      margin: 22px auto 40px;
    }
    h1 {
      margin: 0 0 6px;
      font-size: clamp(26px, 4vw, 38px);
      letter-spacing: 0;
    }
    .subtle { color: var(--muted); }
    .toolbar {
      display: flex;
      gap: 10px;
      align-items: center;
      flex-wrap: wrap;
      margin-top: 16px;
    }
    .toolbar input {
      flex: 1 1 360px;
      min-width: 0;
      height: 40px;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 0 12px;
      color: var(--ink);
      background: #fbfcfd;
      font: 13px ui-monospace, SFMono-Regular, Consolas, monospace;
    }
    .toolbar button {
      height: 40px;
      border: 1px solid var(--accent);
      border-radius: 8px;
      padding: 0 14px;
      color: var(--accent);
      background: #fff;
      font-weight: 700;
      cursor: pointer;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 14px;
      margin-bottom: 18px;
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 16px;
      min-height: 128px;
    }
    .label {
      color: var(--muted);
      font-size: 13px;
      font-weight: 700;
      text-transform: uppercase;
    }
    .value {
      margin-top: 10px;
      font-size: 28px;
      font-weight: 700;
      line-height: 1.1;
    }
    .ok { color: var(--ok); }
    .warn { color: var(--warn); }
    .bad { color: var(--bad); }
    table {
      width: 100%;
      border-collapse: collapse;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
    }
    th, td {
      padding: 12px 14px;
      text-align: left;
      border-bottom: 1px solid var(--line);
      vertical-align: top;
      font-size: 14px;
    }
    th {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      background: #fbfcfd;
    }
    tr:last-child td { border-bottom: 0; }
    code {
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 13px;
    }
    .pill {
      display: inline-block;
      padding: 3px 8px;
      border-radius: 999px;
      border: 1px solid var(--line);
      font-size: 12px;
      font-weight: 700;
      background: #f9fafb;
    }
    .pill.ok { border-color: #bfe4cf; background: #edf8f1; }
    .pill.bad { border-color: #efc5c5; background: #fff0f0; }
    @media (max-width: 760px) {
      .grid { grid-template-columns: 1fr; }
      th:nth-child(6), td:nth-child(6) { display: none; }
    }
  </style>
</head>
<body>
  <header>
    <h1>TunnelDesktop Relay</h1>
    <div class="subtle">Azure WebSocket relay at <code>/relay/ws</code>. Status updates stream live over WebSocket.</div>
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
        <div class="label">Home agent</div>
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
          <th>Home Agent</th>
          <th>Phone IP</th>
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
    const pageRoom = {{roomJson}};

    function pill(ok, text) {
      return `<span class="pill ${ok ? "ok" : "bad"}">${text}</span>`;
    }

    function esc(value) {
      return String(value ?? "").replace(/[&<>"']/g, char => ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;"
      }[char]));
    }

    function fmt(value) {
      if (!value) return "";
      return new Date(value).toLocaleString();
    }

    function setValue(node, text, cls) {
      node.className = "value " + cls;
      node.textContent = text;
    }

    function relayRoomUrl(room) {
      if (!room) return `${location.origin}/relay/`;
      return `${location.origin}/relay/${encodeURIComponent(room)}`;
    }

    function setRoomUrl(room) {
      roomUrl.value = relayRoomUrl(room);
    }

    function render(data) {
      try {
        const rooms = data.rooms || [];
        const waitingAgents = rooms.reduce((sum, r) => sum + (r.waiting_agents || 0), 0);
        const activePairs = rooms.reduce((sum, r) => sum + (r.active_pairs || 0), 0);
        const homeAgents = rooms.filter(r => r.home_agent_connected).length;

        setValue(workStatus, waitingAgents + activePairs > 0 ? "Connected" : "Waiting", waitingAgents + activePairs > 0 ? "ok" : "warn");
        workDetail.textContent = `${waitingAgents} idle work sockets, ${activePairs} paired streams.`;
        setValue(homeStatus, homeAgents > 0 ? "Connected" : "Waiting", homeAgents > 0 ? "ok" : "warn");
        const hotspotIps = rooms.map(r => r.home_agent_hotspot_ip).filter(Boolean);
        homeDetail.textContent = `${homeAgents} Android home-agent status connection${homeAgents === 1 ? "" : "s"}.` +
          (hotspotIps.length ? ` Phone IP: ${hotspotIps.join(", ")}.` : "");
        streamStatus.textContent = activePairs.toString();
        streamDetail.textContent = activePairs === 0 ? "No active RDP streams." : `${activePairs} RDP stream${activePairs === 1 ? "" : "s"} bridged.`;

        if (rooms.length === 0) {
          roomsBody.innerHTML = '<tr><td colspan="6" class="subtle">No token rooms have connected yet.</td></tr>';
          return;
        }
        roomsBody.innerHTML = rooms.map(r => {
          const workConnected = (r.waiting_agents || 0) + (r.active_pairs || 0) > 0;
          const hotspotIp = r.home_agent_hotspot_ip || "";
          const privateIps = r.home_agent_private_ips || [];
          return `<tr>
            <td><code>${esc(r.id)}</code></td>
            <td>${pill(workConnected, workConnected ? "connected" : "waiting")}<br><span class="subtle">${r.waiting_agents || 0} idle<br>${esc(fmt(r.last_agent_connected_at))}</span></td>
            <td>${pill(!!r.home_agent_connected, r.home_agent_connected ? "connected" : "waiting")}<br><span class="subtle">${esc(r.home_agent_remote || "")}<br>${esc(fmt(r.home_agent_connected_at))}</span></td>
            <td>${hotspotIp ? `<code>${esc(hotspotIp)}</code>` : '<span class="subtle">not reported</span>'}<br><span class="subtle">status only; no Android RDP listener${privateIps.length ? "<br>" + privateIps.map(esc).join("<br>") : ""}</span></td>
            <td>${r.active_pairs || 0}<br><span class="subtle">${r.total_pairs || 0} total</span></td>
            <td><span class="subtle">${esc(r.last_client_remote || "")}<br>${esc(fmt(r.last_client_connected_at))}</span></td>
          </tr>`;
        }).join("");
      } catch (error) {
        setValue(workStatus, "Error", "bad");
        setValue(homeStatus, "Error", "bad");
        workDetail.textContent = error.message;
        homeDetail.textContent = error.message;
        roomsBody.innerHTML = `<tr><td colspan="5" class="bad">${error.message}</td></tr>`;
      }
    }

    function connectDashboard() {
      const scheme = location.protocol === "https:" ? "wss:" : "ws:";
      const roomPath = pageRoom ? `/relay/${encodeURIComponent(pageRoom)}/ws` : "/relay/ws";
      const socket = new WebSocket(`${scheme}//${location.host}${roomPath}?role=dashboard`);
      socket.onopen = () => {
        workDetail.textContent = "Connected to live relay status.";
        homeDetail.textContent = "Connected to live relay status.";
      };
      socket.onmessage = event => render(JSON.parse(event.data));
      socket.onclose = () => {
        setValue(workStatus, "Reconnecting", "warn");
        setValue(homeStatus, "Reconnecting", "warn");
        workDetail.textContent = "Dashboard status socket closed. Reconnecting...";
        homeDetail.textContent = "Dashboard status socket closed. Reconnecting...";
        setTimeout(connectDashboard, 1500);
      };
      socket.onerror = () => socket.close();
    }

    connectDashboard();
    setRoomUrl(pageRoom);
    copyRoom.addEventListener("click", async () => {
      roomUrl.select();
      await navigator.clipboard.writeText(roomUrl.value);
    });
  </script>
</body>
</html>
""";
}

sealed class RelayHub
{
    private readonly ConcurrentDictionary<string, RelayRoom> _rooms = new();
    private readonly ConcurrentDictionary<Guid, DashboardClient> _dashboards = new();
    private readonly ILogger<RelayHub> _log;

    public RelayHub(ILogger<RelayHub> log)
    {
        _log = log;
    }

    public async Task ServeAgentAsync(string token, WebSocket socket, string remote, CancellationToken abort)
    {
        var room = RoomFor(token);
        var waiting = room.EnqueueAgent(socket, remote);
        _log.LogInformation("agent waiting room={Room} remote={Remote}", room.Id, remote);
        NotifyDashboards();

        using var reg = abort.Register(() => waiting.TryCancel());
        try
        {
            var peer = await waiting.WaitAsync();
            _log.LogInformation("pairing room={Room} agent={AgentRemote} client={ClientRemote}", room.Id, remote, peer.Remote);
            await SendStartAsync(socket, abort);
            await SendStartAsync(peer.Socket, abort);
            await room.BridgeAsync(socket, peer.Socket, remote, peer.Remote, peer.Done, NotifyDashboards, abort);
        }
        catch (OperationCanceledException)
        {
        }
        catch (WebSocketException ex)
        {
            _log.LogInformation(ex, "agent websocket ended room={Room} remote={Remote}", room.Id, remote);
        }
        finally
        {
            room.RemoveWaiting(waiting);
            NotifyDashboards();
        }
    }

    public async Task ServeClientAsync(string token, WebSocket socket, string remote, CancellationToken abort)
    {
        var room = RoomFor(token);
        var peer = room.TryTakeAgent();
        if (peer is null)
        {
            _log.LogInformation("client rejected without agent room={Room} remote={Remote}", room.Id, remote);
            await socket.CloseAsync(WebSocketCloseStatus.EndpointUnavailable, "no work agent connected", CancellationToken.None);
            return;
        }

        var done = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!peer.TryPair(new HomePeer(socket, remote, done)))
        {
            done.TrySetResult();
            await socket.CloseAsync(WebSocketCloseStatus.EndpointUnavailable, "work agent unavailable", CancellationToken.None);
            return;
        }
        NotifyDashboards();

        using var reg = abort.Register(() => done.TrySetCanceled());
        try
        {
            await done.Task;
        }
        catch (OperationCanceledException)
        {
        }
    }

    public async Task ServeHomeAgentAsync(string token, WebSocket socket, string remote, HomeAgentInfo info, CancellationToken abort)
    {
        var room = RoomFor(token);
        room.HomeAgentConnected(remote, info);
        _log.LogInformation("home agent connected room={Room} remote={Remote} hotspot={HotspotIp}", room.Id, remote, info.HotspotIp);
        NotifyDashboards();
        try
        {
            await DrainUntilCloseAsync(socket, abort);
        }
        finally
        {
            room.HomeAgentDisconnected(remote);
            NotifyDashboards();
            _log.LogInformation("home agent disconnected room={Room} remote={Remote}", room.Id, remote);
        }
    }

    public async Task ServeDashboardAsync(WebSocket socket, string remote, string? roomId, CancellationToken abort)
    {
        var client = new DashboardClient(Guid.NewGuid(), socket, roomId);
        _dashboards[client.Id] = client;
        _log.LogInformation("dashboard connected remote={Remote}", remote);
        try
        {
            await SendDashboardAsync(client, abort);
            await DrainUntilCloseAsync(socket, abort);
        }
        finally
        {
            _dashboards.TryRemove(client.Id, out _);
            await CloseQuietlyAsync(socket);
            _log.LogInformation("dashboard disconnected remote={Remote}", remote);
        }
    }

    public object Snapshot(string? roomId = null)
    {
        var id = string.IsNullOrWhiteSpace(roomId) ? null : RoomId(roomId);
        var rooms = id is null
            ? _rooms.Values.OrderBy(room => room.Id).Select(room => room.Snapshot()).ToArray()
            : _rooms.TryGetValue(id, out var room) ? new[] { room.Snapshot() } : [];
        return new
        {
            service = "TunnelDesktop.Relay",
            time = DateTimeOffset.UtcNow,
            rooms
        };
    }

    private RelayRoom RoomFor(string token)
    {
        var id = RoomId(token);
        return _rooms.GetOrAdd(id, key => new RelayRoom(key, _log));
    }

    private static string RoomId(string token)
    {
        var raw = token.Trim().Trim('/');
        if (raw.Length == 0)
        {
            return "default";
        }

        var builder = new StringBuilder(Math.Min(raw.Length, 64));
        foreach (var c in raw)
        {
            if (builder.Length >= 64)
            {
                break;
            }
            if (c is >= 'A' and <= 'Z')
            {
                builder.Append((char)(c + 32));
            }
            else if (c is >= 'a' and <= 'z' or >= '0' and <= '9' or '-' or '_' or '.')
            {
                builder.Append(c);
            }
            else if (builder.Length == 0 || builder[^1] != '-')
            {
                builder.Append('-');
            }
        }

        var room = builder.ToString().Trim('-', '.');
        return room.Length == 0 ? "default" : room;
    }

    private static async Task SendStartAsync(WebSocket socket, CancellationToken cancellationToken)
    {
        var payload = Encoding.UTF8.GetBytes("start");
        await socket.SendAsync(payload, WebSocketMessageType.Text, true, cancellationToken);
    }

    private static async Task DrainUntilCloseAsync(WebSocket socket, CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(1024);
        try
        {
            while (socket.State == WebSocketState.Open && !cancellationToken.IsCancellationRequested)
            {
                var result = await socket.ReceiveAsync(buffer, cancellationToken);
                if (result.MessageType == WebSocketMessageType.Close)
                {
                    await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "", CancellationToken.None);
                    return;
                }
            }
        }
        catch (OperationCanceledException)
        {
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    private void NotifyDashboards()
    {
        foreach (var client in _dashboards.Values)
        {
            _ = SendDashboardAsync(client, CancellationToken.None);
        }
    }

    private async Task SendDashboardAsync(DashboardClient client, CancellationToken cancellationToken)
    {
        if (client.Socket.State != WebSocketState.Open)
        {
            _dashboards.TryRemove(client.Id, out _);
            return;
        }
        using var timeout = cancellationToken.CanBeCanceled
            ? CancellationTokenSource.CreateLinkedTokenSource(cancellationToken)
            : new CancellationTokenSource();
        timeout.CancelAfter(TimeSpan.FromSeconds(10));

        await client.Lock.WaitAsync(timeout.Token);
        try
        {
            if (client.Socket.State != WebSocketState.Open)
            {
                _dashboards.TryRemove(client.Id, out _);
                return;
            }
            var payload = Encoding.UTF8.GetBytes(System.Text.Json.JsonSerializer.Serialize(Snapshot(client.RoomId)));
            await client.Socket.SendAsync(payload, WebSocketMessageType.Text, true, timeout.Token);
        }
        catch
        {
            _dashboards.TryRemove(client.Id, out _);
        }
        finally
        {
            client.Lock.Release();
        }
    }

    private static async Task CloseQuietlyAsync(WebSocket socket)
    {
        try
        {
            if (socket.State is WebSocketState.Open or WebSocketState.CloseReceived)
            {
                await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "", CancellationToken.None);
            }
        }
        catch
        {
        }
    }
}

sealed class RelayRoom
{
    private readonly object _gate = new();
    private readonly Queue<WaitingAgent> _agents = new();
    private readonly ILogger _log;
    private int _activePairs;
    private long _totalPairs;
    private string? _lastAgentRemote;
    private DateTimeOffset? _lastAgentConnectedAt;
    private DateTimeOffset? _lastAgentDisconnectedAt;
    private string? _homeAgentRemote;
    private string? _homeAgentHotspotIp;
    private string[] _homeAgentPrivateIps = [];
    private DateTimeOffset? _homeAgentConnectedAt;
    private DateTimeOffset? _homeAgentDisconnectedAt;
    private string? _lastClientRemote;
    private DateTimeOffset? _lastClientConnectedAt;
    private DateTimeOffset? _lastClientDisconnectedAt;

    public RelayRoom(string id, ILogger log)
    {
        Id = id;
        _log = log;
    }

    public string Id { get; }

    public WaitingAgent EnqueueAgent(WebSocket socket, string remote)
    {
        var agent = new WaitingAgent(socket, remote);
        lock (_gate)
        {
            PruneClosedAgents();
            _agents.Enqueue(agent);
            _lastAgentRemote = remote;
            _lastAgentConnectedAt = DateTimeOffset.UtcNow;
        }
        return agent;
    }

    public WaitingAgent? TryTakeAgent()
    {
        lock (_gate)
        {
            PruneClosedAgents();
            while (_agents.Count > 0)
            {
                var agent = _agents.Dequeue();
                if (agent.IsOpen)
                {
                    return agent;
                }
            }
            return null;
        }
    }

    public void RemoveWaiting(WaitingAgent waiting)
    {
        lock (_gate)
        {
            _lastAgentDisconnectedAt = DateTimeOffset.UtcNow;
        }
    }

    public void HomeAgentConnected(string remote, HomeAgentInfo info)
    {
        lock (_gate)
        {
            _homeAgentRemote = remote;
            _homeAgentHotspotIp = info.HotspotIp;
            _homeAgentPrivateIps = info.PrivateIps;
            _homeAgentConnectedAt = DateTimeOffset.UtcNow;
        }
    }

    public void HomeAgentDisconnected(string remote)
    {
        lock (_gate)
        {
            if (_homeAgentRemote == remote)
            {
                _homeAgentDisconnectedAt = DateTimeOffset.UtcNow;
                _homeAgentRemote = null;
                _homeAgentHotspotIp = null;
                _homeAgentPrivateIps = [];
                _homeAgentConnectedAt = null;
            }
        }
    }

    public async Task BridgeAsync(WebSocket agent, WebSocket client, string agentRemote, string clientRemote, TaskCompletionSource clientDone, Action stateChanged, CancellationToken abort)
    {
        lock (_gate)
        {
            _activePairs++;
            _totalPairs++;
            _lastClientRemote = clientRemote;
            _lastClientConnectedAt = DateTimeOffset.UtcNow;
            _lastClientDisconnectedAt = null;
        }
        stateChanged();
        try
        {
            using var cts = CancellationTokenSource.CreateLinkedTokenSource(abort);
            var left = PumpAsync(agent, client, cts.Token);
            var right = PumpAsync(client, agent, cts.Token);
            await Task.WhenAny(left, right);
            cts.Cancel();
            await Task.WhenAll(SwallowAsync(left), SwallowAsync(right));
        }
        finally
        {
            lock (_gate)
            {
                if (_activePairs > 0)
                {
                    _activePairs--;
                }
                _lastAgentDisconnectedAt = DateTimeOffset.UtcNow;
                _lastClientDisconnectedAt = DateTimeOffset.UtcNow;
            }
            await CloseQuietlyAsync(agent);
            await CloseQuietlyAsync(client);
            clientDone.TrySetResult();
            stateChanged();
            _log.LogInformation("bridge closed room={Room} agent={AgentRemote} client={ClientRemote}", Id, agentRemote, clientRemote);
        }
    }

    public object Snapshot()
    {
        lock (_gate)
        {
            PruneClosedAgents();
            return new
            {
                id = Id,
                waiting_agents = _agents.Count,
                active_pairs = _activePairs,
                total_pairs = _totalPairs,
                last_agent_remote = _lastAgentRemote,
                last_agent_connected_at = _lastAgentConnectedAt,
                last_agent_disconnected_at = _lastAgentDisconnectedAt,
                home_agent_connected = _homeAgentRemote is not null,
                home_agent_remote = _homeAgentRemote,
                home_agent_hotspot_ip = _homeAgentHotspotIp,
                home_agent_private_ips = _homeAgentPrivateIps,
                home_agent_connected_at = _homeAgentConnectedAt,
                home_agent_disconnected_at = _homeAgentDisconnectedAt,
                last_client_remote = _lastClientRemote,
                last_client_connected_at = _lastClientConnectedAt,
                last_client_disconnected_at = _lastClientDisconnectedAt
            };
        }
    }

    private void PruneClosedAgents()
    {
        var count = _agents.Count;
        for (var i = 0; i < count; i++)
        {
            var agent = _agents.Dequeue();
            if (agent.IsOpen)
            {
                _agents.Enqueue(agent);
            }
        }
    }

    private static async Task PumpAsync(WebSocket source, WebSocket destination, CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(64 * 1024);
        try
        {
            while (source.State == WebSocketState.Open && destination.State == WebSocketState.Open && !cancellationToken.IsCancellationRequested)
            {
                var result = await source.ReceiveAsync(buffer, cancellationToken);
                if (result.MessageType == WebSocketMessageType.Close)
                {
                    return;
                }
                if (result.MessageType != WebSocketMessageType.Binary)
                {
                    continue;
                }
                await destination.SendAsync(new ArraySegment<byte>(buffer, 0, result.Count), WebSocketMessageType.Binary, result.EndOfMessage, cancellationToken);
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    private static async Task SwallowAsync(Task task)
    {
        try
        {
            await task;
        }
        catch
        {
        }
    }

    private static async Task CloseQuietlyAsync(WebSocket socket)
    {
        try
        {
            if (socket.State is WebSocketState.Open or WebSocketState.CloseReceived)
            {
                await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "", CancellationToken.None);
            }
        }
        catch
        {
        }
    }
}

sealed class WaitingAgent
{
    private readonly TaskCompletionSource<HomePeer> _paired = new(TaskCreationOptions.RunContinuationsAsynchronously);

    public WaitingAgent(WebSocket socket, string remote)
    {
        Socket = socket;
        Remote = remote;
    }

    public WebSocket Socket { get; }
    public string Remote { get; }
    public bool IsOpen => Socket.State == WebSocketState.Open;

    public Task<HomePeer> WaitAsync() => _paired.Task;
    public bool TryPair(HomePeer peer) => _paired.TrySetResult(peer);
    public void TryCancel() => _paired.TrySetCanceled();
}

sealed record HomePeer(WebSocket Socket, string Remote, TaskCompletionSource Done);

sealed record HomeAgentInfo(string? HotspotIp, string[] PrivateIps);

sealed record DashboardClient(Guid Id, WebSocket Socket, string? RoomId)
{
    public SemaphoreSlim Lock { get; } = new(1, 1);
}

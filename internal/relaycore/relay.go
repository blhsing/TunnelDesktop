package relaycore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/hashicorp/yamux"

	"tunneldesktop/internal/tunnel"
)

type Relay struct {
	cfg       Config
	tlsConfig *tls.Config
	log       tunnel.LogFunc

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	tlsListener net.Listener
	rawListener net.Listener
	tlsSlots    chan struct{}
	streamSlots chan struct{}
	allowlist   *tunnel.AllowList

	mu        sync.Mutex
	agent     *yamux.Session
	agentWake chan struct{}
	status    string
}

func New(cfg Config, log tunnel.LogFunc) (*Relay, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config
	var err error
	if cfg.CAPEM != "" || cfg.CertPEM != "" || cfg.KeyPEM != "" {
		tlsConfig, err = tunnel.ServerTLSConfigFromPEM(cfg.CAPEM, cfg.CertPEM, cfg.KeyPEM)
	} else {
		tlsConfig, err = tunnel.ServerTLSConfig(cfg.CAFile, cfg.CertFile, cfg.KeyFile)
	}
	if err != nil {
		return nil, err
	}
	allowlist, err := tunnel.ParseAllowList(cfg.RawAllowlist)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = tunnel.NoopLog
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Relay{
		cfg:         cfg,
		tlsConfig:   tlsConfig,
		log:         log,
		ctx:         ctx,
		cancel:      cancel,
		tlsSlots:    make(chan struct{}, cfg.MaxTLSConnections),
		streamSlots: make(chan struct{}, cfg.MaxHomeStreams),
		allowlist:   allowlist,
		agentWake:   make(chan struct{}),
		status:      "stopped",
	}, nil
}

func (r *Relay) Start() error {
	r.mu.Lock()
	if r.status == "running" {
		r.mu.Unlock()
		return nil
	}
	r.status = "starting"
	r.mu.Unlock()

	tlsListener, err := net.Listen("tcp", r.cfg.ListenAddr)
	if err != nil {
		r.setStatus("stopped")
		return fmt.Errorf("listen TLS %s: %w", r.cfg.ListenAddr, err)
	}
	r.tlsListener = tlsListener
	r.log("relay TLS listening on %s", tlsListener.Addr())
	r.wg.Add(1)
	go r.acceptTLS()

	if r.cfg.RawRDPAddr != "" {
		rawListener, err := net.Listen("tcp", r.cfg.RawRDPAddr)
		if err != nil {
			_ = tlsListener.Close()
			r.setStatus("stopped")
			return fmt.Errorf("listen raw RDP %s: %w", r.cfg.RawRDPAddr, err)
		}
		r.rawListener = rawListener
		r.log("raw RDP listening on %s", rawListener.Addr())
		r.wg.Add(1)
		go r.acceptRaw()
	}

	r.setStatus("running")
	return nil
}

func (r *Relay) Stop() error {
	r.cancel()
	if r.tlsListener != nil {
		_ = r.tlsListener.Close()
	}
	if r.rawListener != nil {
		_ = r.rawListener.Close()
	}
	r.mu.Lock()
	if r.agent != nil {
		_ = r.agent.Close()
		r.agent = nil
	}
	r.mu.Unlock()
	r.wg.Wait()
	r.setStatus("stopped")
	return nil
}

func (r *Relay) Status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Relay) setStatus(status string) {
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
}

func (r *Relay) acceptTLS() {
	defer r.wg.Done()
	defer r.recover("TLS accept loop")
	for {
		conn, err := r.tlsListener.Accept()
		if err != nil {
			if r.ctx.Err() == nil {
				r.log("TLS accept failed: %v", err)
			}
			return
		}
		select {
		case r.tlsSlots <- struct{}{}:
			r.wg.Add(1)
			go r.handleTLS(conn)
		default:
			r.log("rejecting TLS connection from %s: connection limit reached", conn.RemoteAddr())
			_ = conn.Close()
		}
	}
}

func (r *Relay) handleTLS(rawConn net.Conn) {
	defer r.wg.Done()
	defer func() { <-r.tlsSlots }()
	defer r.recover("TLS connection")

	tlsConn := tls.Server(rawConn, r.tlsConfig)
	if err := tlsConn.HandshakeContext(r.ctx); err != nil {
		r.log("TLS handshake from %s failed: %v", rawConn.RemoteAddr(), err)
		_ = rawConn.Close()
		return
	}

	auth, err := tunnel.ReadAuth(r.ctx, tlsConn, r.cfg.Token, tunnel.RoleAgent, tunnel.RoleClient)
	if err != nil {
		r.log("auth from %s failed: %v", rawConn.RemoteAddr(), err)
		_ = tlsConn.Close()
		return
	}

	switch auth.Role {
	case tunnel.RoleAgent:
		r.serveAgent(tlsConn)
	case tunnel.RoleClient:
		r.serveHomeConn(tlsConn, "tls client")
	default:
		_ = tlsConn.Close()
	}
}

func (r *Relay) acceptRaw() {
	defer r.wg.Done()
	defer r.recover("raw RDP accept loop")
	for {
		conn, err := r.rawListener.Accept()
		if err != nil {
			if r.ctx.Err() == nil {
				r.log("raw RDP accept failed: %v", err)
			}
			return
		}
		if !r.cfg.DisableRawAllowlist && !r.allowlist.ContainsAddr(conn.RemoteAddr()) {
			r.log("rejecting raw RDP connection from %s: not in allowlist", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.serveHomeConn(conn, "raw RDP")
		}()
	}
}

func (r *Relay) serveAgent(conn net.Conn) {
	defer r.recover("agent session")
	session, err := yamux.Client(conn, tunnel.YamuxConfig())
	if err != nil {
		r.log("create agent yamux client failed: %v", err)
		_ = conn.Close()
		return
	}
	r.registerAgent(session)
	r.log("agent connected from %s", conn.RemoteAddr())

	select {
	case <-r.ctx.Done():
		_ = session.Close()
	case <-session.CloseChan():
	}

	r.clearAgent(session)
	r.log("agent disconnected from %s", conn.RemoteAddr())
}

func (r *Relay) serveHomeConn(homeConn net.Conn, label string) {
	defer r.recover(label)
	select {
	case r.streamSlots <- struct{}{}:
		defer func() { <-r.streamSlots }()
	default:
		r.log("rejecting %s connection from %s: stream limit reached", label, homeConn.RemoteAddr())
		_ = homeConn.Close()
		return
	}

	agentStream, err := r.openAgentStream()
	if err != nil {
		r.log("closing %s connection from %s: %v", label, homeConn.RemoteAddr(), err)
		_ = homeConn.Close()
		return
	}
	r.log("bridging %s connection from %s", label, homeConn.RemoteAddr())
	tunnel.Pipe(homeConn, agentStream)
	r.log("closed %s connection from %s", label, homeConn.RemoteAddr())
}

func (r *Relay) registerAgent(session *yamux.Session) {
	r.mu.Lock()
	old := r.agent
	r.agent = session
	close(r.agentWake)
	r.agentWake = make(chan struct{})
	r.mu.Unlock()
	if old != nil && old != session {
		_ = old.Close()
	}
}

func (r *Relay) clearAgent(session *yamux.Session) {
	r.mu.Lock()
	if r.agent == session {
		r.agent = nil
	}
	r.mu.Unlock()
}

func (r *Relay) openAgentStream() (net.Conn, error) {
	wait, err := r.cfg.AgentWaitDuration()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, wait)
	defer cancel()

	for {
		session, err := r.waitForAgent(ctx)
		if err != nil {
			return nil, err
		}
		stream, err := session.OpenStream()
		if err == nil {
			return stream, nil
		}
		r.log("open agent stream failed: %v", err)
		r.clearAgent(session)
	}
}

func (r *Relay) waitForAgent(ctx context.Context) (*yamux.Session, error) {
	for {
		r.mu.Lock()
		session := r.agent
		wake := r.agentWake
		r.mu.Unlock()
		if session != nil && !session.IsClosed() {
			return session, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("no agent connected before timeout")
			}
			return nil, ctx.Err()
		case <-wake:
		}
	}
}

func (r *Relay) recover(scope string) {
	if recovered := recover(); recovered != nil {
		r.log("panic in %s: %v", scope, recovered)
	}
}

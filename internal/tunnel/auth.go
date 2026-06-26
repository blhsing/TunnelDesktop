package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"
)

const maxAuthLineBytes = 4096

type AuthRequest struct {
	Token string `json:"token"`
	Role  string `json:"role"`
}

func SendAuth(ctx context.Context, conn net.Conn, token, role string) error {
	if token == "" {
		return errors.New("token is required")
	}
	if role != RoleAgent && role != RoleClient {
		return fmt.Errorf("invalid role %q", role)
	}
	restoreDeadline := setConnDeadline(ctx, conn, 10*time.Second)
	defer restoreDeadline()

	payload, err := json.Marshal(AuthRequest{Token: token, Role: role})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = conn.Write(payload)
	if err != nil {
		return fmt.Errorf("send auth: %w", err)
	}
	return nil
}

func ReadAuth(ctx context.Context, conn net.Conn, expectedToken string, allowedRoles ...string) (AuthRequest, error) {
	if expectedToken == "" {
		return AuthRequest{}, errors.New("expected token is required")
	}
	restoreDeadline := setConnDeadline(ctx, conn, 10*time.Second)
	defer restoreDeadline()

	line, err := readLineNoBuffer(conn, maxAuthLineBytes)
	if err != nil {
		return AuthRequest{}, fmt.Errorf("read auth: %w", err)
	}
	var req AuthRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return AuthRequest{}, fmt.Errorf("decode auth: %w", err)
	}
	if req.Token != expectedToken {
		return AuthRequest{}, errors.New("invalid token")
	}
	if !slices.Contains(allowedRoles, req.Role) {
		return AuthRequest{}, fmt.Errorf("role %q is not allowed", req.Role)
	}
	return req, nil
}

func readLineNoBuffer(conn net.Conn, max int) ([]byte, error) {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	for len(buf) < max {
		n, err := conn.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				return buf, nil
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("auth line exceeded %d bytes", max)
}

func setConnDeadline(ctx context.Context, conn net.Conn, max time.Duration) func() {
	deadline := time.Now().Add(max)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	return func() {
		_ = conn.SetDeadline(time.Time{})
	}
}

package tunnel

import (
	"context"
	"net"
	"testing"
)

func TestAuthRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- SendAuth(context.Background(), a, "secret", RoleAgent)
	}()
	req, err := ReadAuth(context.Background(), b, "secret", RoleAgent)
	if err != nil {
		t.Fatalf("ReadAuth: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendAuth: %v", err)
	}
	if req.Role != RoleAgent || req.Token != "secret" {
		t.Fatalf("unexpected auth request: %#v", req)
	}
}

func TestAuthRejectsWrongToken(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		_ = SendAuth(context.Background(), a, "wrong", RoleClient)
	}()
	if _, err := ReadAuth(context.Background(), b, "secret", RoleClient); err == nil {
		t.Fatal("expected wrong token to be rejected")
	}
}

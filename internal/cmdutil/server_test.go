package cmdutil

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// listenLocal returns a TCP listener bound to a random local port plus
// the matching http.Server preconfigured with a no-op handler.
func listenLocal(t *testing.T, handler http.Handler) (*net.TCPListener, *http.Server) {
	t.Helper()
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Addr:    ln.Addr().String(),
		Handler: handler,
	}
	return ln, srv
}

func TestRunServer_ServesUntilContextCancel(t *testing.T) {
	ln, srv := listenLocal(t, nil)
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, srv, ln, zap.NewNop(), 2*time.Second)
	}()

	// Give the server a moment to start accepting.
	time.Sleep(50 * time.Millisecond)

	// Confirm it accepts requests.
	resp, err := http.Get("http://" + srv.Addr + "/")
	if err != nil {
		t.Fatalf("GET while running: %v", err)
	}
	resp.Body.Close()

	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("RunServer returned %v, want nil or ErrServerClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunServer did not return within grace period after context cancel")
	}
}

func TestRunServer_DrainsInFlightRequestsBeforeShutdown(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	releaseHandler := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		<-releaseHandler // block until test releases
		w.WriteHeader(http.StatusOK)
	})

	ln, srv := listenLocal(t, handler)
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, srv, ln, zap.NewNop(), 2*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)

	// Fire an in-flight request that will block in the handler.
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr + "/")
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	// Wait until the handler is in-flight.
	time.Sleep(50 * time.Millisecond)

	// Trigger shutdown. RunServer should call srv.Shutdown which waits
	// for the in-flight request before returning.
	cancel()

	// Release the in-flight handler so shutdown can drain.
	close(releaseHandler)

	select {
	case resp := <-respCh:
		if resp.StatusCode != http.StatusOK {
			t.Errorf("in-flight request status = %d, want 200", resp.StatusCode)
		}
		resp.Body.Close()
	case err := <-errCh:
		t.Fatalf("in-flight request errored before shutdown drain: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete during shutdown drain")
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("RunServer returned %v after shutdown, want nil or ErrServerClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunServer did not return after shutdown drained")
	}

	wg.Wait()
}

func TestRunServer_ReturnsListenError(t *testing.T) {
	// Bind a port, then try to start a server on the same port to
	// force an immediate Serve error path.
	ln, srv := listenLocal(t, nil)
	defer ln.Close()

	dup, err := net.Listen("tcp", srv.Addr)
	if err == nil {
		dup.Close()
	}
	// dup may have failed to bind; either way the test setup is fine.

	srv2 := &http.Server{Addr: srv.Addr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a listener whose Accept will fail immediately by closing it
	// before passing in.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	ln2.Close() // close before passing → Serve returns immediately

	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, srv2, ln2, zap.NewNop(), 2*time.Second)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Errorf("expected non-nil error from RunServer when listener closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunServer did not return after listener-closed error")
	}
}

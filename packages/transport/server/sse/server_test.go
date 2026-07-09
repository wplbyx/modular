package sse

import (
	"context"
	"testing"
	"time"
)

func TestServerShutdownUnblocksStartup(t *testing.T) {
	srv := NewServer(1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Startup(context.Background())
	}()

	requireEventually(t, func() bool { return srv.IsStarted() }, "server did not start")

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Startup() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Startup() was not unblocked by Shutdown()")
	}
}

func TestServerReplaceClientClosesOldConnection(t *testing.T) {
	srv := NewServer(1)
	old := &Client{ID: "client-1", MsgChan: make(chan Message, 1)}
	replacement := &Client{ID: "client-1", MsgChan: make(chan Message, 1)}

	srv.addClient("client-1", old)
	srv.addClient("client-1", replacement)

	select {
	case _, ok := <-old.MsgChan:
		if ok {
			t.Fatal("old client channel is still open")
		}
	default:
		t.Fatal("old client channel was not closed")
	}

	if got := srv.GetClientCount(); got != 1 {
		t.Fatalf("client count = %d, want 1", got)
	}
	if !srv.Publish("client-1", Message{Event: "test", Data: "new"}) {
		t.Fatal("replacement client did not receive publish")
	}
}

func TestServerPublishAfterShutdownDoesNotPanic(t *testing.T) {
	srv := NewServer(1)
	srv.addClient("client-1", &Client{ID: "client-1", MsgChan: make(chan Message, 1)})

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish/Notify panicked after Shutdown(): %v", r)
		}
	}()

	if srv.Publish("client-1", Message{Event: "test", Data: "closed"}) {
		t.Fatal("Publish returned true after shutdown")
	}
	srv.Notify(Message{Event: "test", Data: "closed"})
}

func requireEventually(t *testing.T, fn func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fn() {
		t.Fatal(msg)
	}
}

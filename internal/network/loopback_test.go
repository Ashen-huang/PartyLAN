package network

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestLoopbackNetworkDialListen(t *testing.T) {
	net := NewLoopbackNetwork("host-node")

	listener, err := net.Listen(9001)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	serverErrCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.Close()

		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErrCh <- err
			return
		}

		if string(buf) != "hello" {
			serverErrCh <- io.ErrUnexpectedEOF
			return
		}

		_, err = conn.Write([]byte("world"))
		serverErrCh <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client, err := net.Dial(ctx, LoopbackNodeID, 9001)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	reply := make([]byte, 5)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}

	if string(reply) != "world" {
		t.Fatalf("expected world, got %q", string(reply))
	}

	if err := <-serverErrCh; err != nil {
		t.Fatalf("server side error: %v", err)
	}
}

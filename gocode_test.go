package main_test

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestCancellation(t *testing.T) {
	const testServerAddress = "127.0.0.1:38383"

	var buffer buffer
	defer func() {
		if t.Failed() {
			t.Log("\n" + string(buffer.text))
		}
	}()

	gocode, err := exec.LookPath("gocode")
	if err != nil {
		t.Skip(err)
	}
	t.Log("running test with ", gocode)

	serverCtx, serverCancel := context.WithCancel(context.Background())

	// start the server
	cmd := exec.CommandContext(serverCtx, gocode, "-s", "-debug",
		"-sock", "tcp",
		"-addr", testServerAddress,
	)
	cmd.Stderr, cmd.Stdout = buffer.prefixed("server | "), buffer.prefixed("server | ")

	// stop server after five seconds
	go func() {
		time.Sleep(5 * time.Second)
		serverCancel()
	}()

	// start server
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	runClients(t, testServerAddress)

	time.Sleep(time.Second)

	// cancel server when any of the clients fails
	serverCancel()

	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

}

func runClients(t *testing.T, serverAddr string) {
	var buffer buffer
	defer func() {
		if t.Failed() {
			t.Log("\n" + string(buffer.text))
		}
	}()

	clientCtx, clientCancel := context.WithCancel(context.Background())
	var clientGroup errgroup.Group

	// start bunch of clients
	for i := 0; i < 10; i++ {
		offset := i * 10
		clientGroup.Go(func() error {
			cmd := exec.CommandContext(clientCtx, "gocode",
				"-sock", "tcp",
				"-addr", serverAddr,
				"autocomplete", "gocode_test.go", strconv.Itoa(offset))
			cmd.Stderr, cmd.Stdout = buffer.prefixed("client | "), buffer.prefixed("client | ")
			err := cmd.Run()
			if err != nil {
				return fmt.Errorf("client offset=%d: %v", offset, err)
			}
			return nil
		})
	}

	// wait for a bit
	time.Sleep(150 * time.Millisecond)

	// cancel all clients
	clientCancel()

	err := clientGroup.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

type buffer struct {
	mu   sync.Mutex
	text []byte
}

func (b *buffer) Write(prefix string, data []byte) (int, error) {
	b.mu.Lock()
	b.text = append(b.text, []byte(prefix)...)
	b.text = append(b.text, data...)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.text = append(b.text, '\n')
	}
	b.mu.Unlock()
	return len(data), nil
}

func (buffer *buffer) prefixed(prefix string) *writer {
	return &writer{
		prefix: prefix,
		buffer: buffer,
	}
}

type writer struct {
	prefix string
	buffer *buffer
}

func (w *writer) Write(data []byte) (int, error) {
	return w.buffer.Write(w.prefix, data)
}

package main_test

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// use `go build . && go test .` to run this
func TestCancellation_Panic(t *testing.T) {
	// checks that neither server nor client panic on cancellation

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
	cmd := exec.CommandContext(serverCtx, gocode, "-s", // "-debug",
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

	_ = cmd.Wait()

	if strings.Contains(string(buffer.text), "panic") || strings.Contains(string(buffer.text), "PANIC") {
		t.Fail()
	}
}

func runClients(t *testing.T, serverAddr string) {
	const N = 10
	const testFile = "gocode_test.go"

	var buffer buffer
	defer func() {
		if t.Failed() {
			t.Log("\n" + string(buffer.text))
		}
	}()

	clientCtx, clientCancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(N)

	// start bunch of clients
	for i := 0; i < N; i++ {
		offset := i * 5
		stdout := buffer.prefixed(fmt.Sprintf("client %d |", i))
		go func() {
			defer wg.Done()

			cmd := exec.CommandContext(clientCtx, "gocode",
				"-sock", "tcp",
				"-addr", serverAddr,
				"-in", testFile,
				"autocomplete", testFile, strconv.Itoa(offset))

			cmd.Stderr, cmd.Stdout = stdout, stdout
			_ = cmd.Run()
		}()
	}

	// wait for a bit
	time.Sleep(300 * time.Millisecond)

	// cancel all clients
	clientCancel()

	wg.Wait()

	if strings.Contains(string(buffer.text), "panic") || strings.Contains(string(buffer.text), "PANIC") {
		t.Fail()
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

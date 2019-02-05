package main_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func compile(t testing.TB, pkg string) (executable string, cleanup func()) {
	t.Helper()

	dir, err := ioutil.TempDir("", "gocode")
	if err != nil {
		t.Fatal(err)
	}

	cleanup = func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}

	executable = filepath.Join(dir, path.Base(pkg)+".exe")
	out, err := exec.Command("go", "build", "-o", executable, pkg).CombinedOutput()
	if err != nil {
		cleanup()
		t.Error(string(out))
		t.Fatal(err)
	}

	return executable, cleanup
}

// TestCancellation_Panic checks that neither client nor server panic on cancellation.
func TestCancellation_Panic(t *testing.T) {
	const testServerAddress = "127.0.0.1:38383"

	var buffer buffer
	defer func() {
		if t.Failed() {
			t.Log("\n" + string(buffer.text))
		}
	}()

	gocode, cleanup := compile(t, "github.com/stamblerre/gocode")
	defer cleanup()

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

	runClients(t, gocode, testServerAddress)

	time.Sleep(time.Second)

	// cancel server when any of the clients fails
	serverCancel()

	_ = cmd.Wait()

	if strings.Contains(strings.ToLower(string(buffer.text)), "panic") {
		t.Fail()
	}
}

func runClients(t *testing.T, gocode, serverAddr string) {
	const N = 10
	const testFile = "gocode_test.go"

	var buffer buffer
	defer func() {
		if t.Failed() {
			t.Log("\n" + string(buffer.text))
		}
	}()

	clientCtx, cancelClient := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(N)

	// start bunch of clients
	for i := 0; i < N; i++ {
		offset := i * 5
		stdout := buffer.prefixed(fmt.Sprintf("client %d |", i))
		go func() {
			defer wg.Done()

			cmd := exec.CommandContext(clientCtx, gocode,
				"-sock", "tcp",
				"-addr", serverAddr,
				"-in", testFile,
				"autocomplete", testFile, strconv.Itoa(offset))

			cmd.Stderr, cmd.Stdout = stdout, stdout
			_ = cmd.Run()
		}()
	}

	time.Sleep(300 * time.Millisecond)
	cancelClient()

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

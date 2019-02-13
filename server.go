package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/keegancsmith/rpc"
	"github.com/stamblerre/gocode/internal/suggest"
)

func doServer(ctx context.Context, _ bool) {
	for _, v := range strings.Fields(suggest.GoosList) {
		suggest.KnownOS[v] = true
	}
	for _, v := range strings.Fields(suggest.GoarchList) {
		suggest.KnownArch[v] = true
	}

	addr := *g_addr
	if *g_sock == "unix" {
		addr = getSocketPath()
	}

	lis, err := net.Listen(*g_sock, addr)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		<-ctx.Done()
		exitServer()
	}()

	if err = rpc.Register(&Server{ctx, false}); err != nil {
		log.Fatal(err)
	}
	rpc.Accept(lis)
}

func exitServer() {
	if *g_sock == "unix" {
		_ = os.Remove(getSocketPath())
	}
	os.Exit(0)
}

type Server struct {
	context context.Context
	cache   bool
}

type AutoCompleteRequest struct {
	Filename           string
	Data               []byte
	Cursor             int
	Context            *suggest.PackedContext
	Source             bool
	Builtin            bool
	IgnoreCase         bool
	UnimportedPackages bool
	FallbackToSource   bool
}

type AutoCompleteReply struct {
	Candidates []suggest.Candidate
	Len        int
}

func (s *Server) AutoComplete(ctx context.Context, req *AutoCompleteRequest, res *AutoCompleteReply) error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("panic: %s\n\n", err)
			debug.PrintStack()

			res.Candidates = []suggest.Candidate{
				{Class: "PANIC", Name: "PANIC", Type: "PANIC"},
			}
		}
	}()

	// cancel any pending request when server is shuting down
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-s.context.Done():
			cancel()
		}
	}()

	if *g_debug {
		var buf bytes.Buffer
		log.Printf("Got autocompletion request for '%s'\n", req.Filename)
		log.Printf("Cursor at: %d\n", req.Cursor)
		if req.Cursor <= len(req.Data) {
			buf.WriteString("-------------------------------------------------------\n")
			buf.Write(req.Data[:req.Cursor])
			buf.WriteString("#")
			buf.Write(req.Data[req.Cursor:])
			log.Print(buf.String())
			log.Println("-------------------------------------------------------")
		}
	}

	now := time.Now()
	cfg := suggest.Config{
		RequestContext:     ctx,
		Context:            req.Context,
		Builtin:            req.Builtin,
		IgnoreCase:         req.IgnoreCase,
		UnimportedPackages: req.UnimportedPackages,
		Logf:               func(string, ...interface{}) {},
	}
	cfg.Logf = func(string, ...interface{}) {}
	if *g_debug {
		cfg.Logf = log.Printf
	}
	candidates, d := cfg.Suggest(req.Filename, req.Data, req.Cursor)
	if candidates == nil {
		candidates = []suggest.Candidate{}
	}
	elapsed := time.Since(now)
	if *g_debug {
		log.Printf("Elapsed duration: %v\n", elapsed)
		log.Printf("Offset: %d\n", res.Len)
		log.Printf("Number of candidates found: %d\n", len(candidates))
		log.Printf("Candidates are:\n")
		for _, c := range candidates {
			log.Printf("  %s\n", c.String())
		}
		log.Println("=======================================================")
	}
	res.Candidates, res.Len = candidates, d
	return nil
}

type ExitRequest struct{}
type ExitReply struct{}

func (s *Server) Exit(ctx context.Context, req *ExitRequest, res *ExitReply) error {
	go func() {
		time.Sleep(time.Second)
		exitServer()
	}()
	return nil
}

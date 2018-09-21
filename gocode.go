package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

var (
	g_is_server = flag.Bool("s", false, "run a server instead of a client")
	g_format    = flag.String("f", "nice", "output format (vim | emacs | nice | csv | json)")
	g_input     = flag.String("in", "", "use this file instead of stdin input")
	g_sock      = flag.String("sock", defaultSocketType, "socket type (unix | tcp | none)")
	g_addr      = flag.String("addr", "127.0.0.1:37373", "address for tcp socket")
	g_debug     = flag.Bool("debug", false, "enable server-side debug mode")
	g_source    = flag.Bool("source", false, "use source importer")
	g_builtin   = flag.Bool("builtin", false, "propose builtin objects")
)

func getSocketPath() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "all"
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("gocode-daemon.%s", user))
}

func usage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s [-s] [-f=<format>] [-in=<path>] [-sock=<type>] [-addr=<addr>]\n"+
			"       <command> [<args>]\n\n",
		os.Args[0])
	fmt.Fprintf(os.Stderr,
		"Flags:\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr,
		"\nCommands:\n"+
			"  autocomplete [<path>] <offset>     main autocompletion command\n"+
			"  exit                               terminate the gocode daemon\n")
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *g_is_server {
		doServer()
	} else {
		doClient()
	}
}

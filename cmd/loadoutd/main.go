// loadoutd is the sync server: it stores OPAQUE encrypted snapshot
// blobs and never parses them.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"loadout.dev/loadout/internal/server"
)

func main() {
	dataDir, addr, corsOrigin, ok := parseArgs(os.Args[1:], os.Stderr)
	if !ok {
		os.Exit(2)
	}
	srv, err := newServer(dataDir, addr, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadoutd: %v\n", err)
		os.Exit(1)
	}
	srv.SetCORSOrigin(corsOrigin)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "loadoutd: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs parses loadoutd's flags: -data (required), -addr
// (default :7777), and -cors-origin (default "", CORS off). When
// -cors-origin is not passed, parseArgs falls back to the
// LOADOUT_CORS_ORIGIN environment variable; the flag wins when both
// are set. ok is false when the flags cannot start the server;
// parseArgs has already printed the reason to stderr.
func parseArgs(args []string, stderr io.Writer) (dataDir, addr, corsOrigin string, ok bool) {
	fs := flag.NewFlagSet("loadoutd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	d := fs.String("data", "", "the server's data directory (required)")
	a := fs.String("addr", ":7777", "the address loadoutd listens on")
	c := fs.String("cors-origin", "", "the browser origin allowed to call this server's API over CORS (default: off)")
	if err := fs.Parse(args); err != nil {
		return "", "", "", false
	}
	if *d == "" {
		fmt.Fprintln(stderr, "loadoutd: -data is required. Fix: pass -data <dir>.")
		return "", "", "", false
	}
	origin := *c
	if origin == "" {
		origin = os.Getenv("LOADOUT_CORS_ORIGIN")
	}
	return *d, *a, origin, true
}

// newServer opens the store at dataDir and builds the Server that
// serves it. On the very first run for dataDir it generates the
// access token and prints it once; on every later run it prints the
// address it is about to listen on, and the token itself never
// appears. It returns a clear error, naming the path, when dataDir
// is not writable.
func newServer(dataDir, addr string, stdout io.Writer) (*server.Server, error) {
	store, err := server.Open(dataDir)
	if err != nil {
		return nil, err
	}
	token, created, err := store.Token()
	if err != nil {
		return nil, err
	}
	if created {
		fmt.Fprintf(stdout, "loadoutd: generated an access token: %s\n", token)
	} else {
		fmt.Fprintf(stdout, "loadoutd: listening on %s\n", addr)
	}
	logger := log.New(stdout, "", log.LstdFlags)
	return server.New(store, token, logger), nil
}

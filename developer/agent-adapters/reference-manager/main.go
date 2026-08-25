// Command reference-manager is the development-time terminal reference input
// tool. It manages native-Agent terminal screenshots (checklist, candidates,
// content hashes, evidence states) and freezes them into incremental work
// orders. It is a local-only dev tool: it never joins the product UI, public
// API or release binaries, and it never starts an Agent CLI or injects input.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/bbsteel/session-insight/internal/reader"
)

//go:embed index.html
var indexHTML string

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprint(w, indexHTML) //nolint:errcheck
}

func resolveReferenceStore(checkoutDir string) (root, note string, store *Store, err error) {
	preferredRoot, err := defaultStoreRoot()
	if err != nil {
		return "", "", nil, err
	}
	root, note, err = ensureStoreRoot(preferredRoot, checkoutFallbackStore(checkoutDir), os.Getenv(StoreRootEnv) == "")
	if err != nil {
		return "", "", nil, err
	}
	resolveAgent := func(agent string) (string, bool) {
		def, ok := reader.AgentDefinition(agent)
		if !ok {
			return "", false
		}
		return def.AgentType, true
	}
	return root, note, newStore(root, resolveAgent), nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify-work-order" {
		os.Exit(runVerifyWorkOrder(os.Args[2:]))
	}

	scanLimit := flag.Int("scan-sessions", 30, "most recent sessions to scan per Agent for candidates")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: terminal-reference [agent]\n")
		fmt.Fprintf(flag.CommandLine.Output(), "       terminal-reference verify-work-order --work-order <WORK_ORDER.md>\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Starts the local terminal Reference Manager (loopback only, random port).\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Without an agent argument all onboarded Agents are available in the picker.\n")
		fmt.Fprintf(flag.CommandLine.Output(), "verify-work-order is the coding-agent preflight; it prints a JSON result code.\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Environment:\n  %s\toverride the reference store root (default ~/.session-insight-dev/terminal-references)\n\n", StoreRootEnv)
		flag.PrintDefaults()
	}
	flag.Parse()

	checkoutDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("resolve checkout directory: %v", err)
	}
	root, note, store, err := resolveReferenceStore(checkoutDir)
	if err != nil {
		log.Fatalf("reference store root: %v", err)
	}
	if note != "" {
		log.Print(note)
	}

	readers := reader.Discover()
	srv := newServer(store, checkoutDir, readers, *scanLimit)

	selected := ""
	if flag.NArg() > 1 {
		log.Fatalf("at most one agent argument is accepted")
	}
	if flag.NArg() == 1 {
		selected = flag.Arg(0)
		if !srv.knownAgent(selected) {
			known := make([]string, 0, len(srv.agents))
			for _, a := range srv.agents {
				known = append(known, a.Type)
			}
			log.Fatalf("unknown agent %q (registered: %s)", selected, strings.Join(known, ", "))
		}
	}

	// Import drop-in files and kick off candidate discovery.
	targets := []string{}
	if selected != "" {
		targets = append(targets, selected)
	} else {
		for _, a := range srv.agents {
			targets = append(targets, a.Type)
		}
	}
	for _, agent := range targets {
		imported, skipped, err := store.ScanDropIns(agent)
		if err != nil {
			log.Printf("drop-in scan %s: %v", agent, err)
		}
		if len(imported) > 0 {
			log.Printf("drop-in scan %s: imported %s", agent, strings.Join(imported, ", "))
		}
		if len(skipped) > 0 {
			log.Printf("drop-in scan %s: skipped %s", agent, strings.Join(skipped, ", "))
		}
		srv.refreshCandidates(agent)
	}

	listener, err := listenLoopback()
	if err != nil {
		log.Fatalf("%v", err)
	}
	url := fmt.Sprintf("http://%s/?agent=%s", listener.Addr().String(), selected)
	log.Printf("reference store: %s", root)
	log.Printf("Ready: %s", url)
	fmt.Printf("Ready: %s\n", url)
	if err := http.Serve(listener, srv.routes()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/packops"
	"github.com/bbsteel/session-insight/internal/reader"
)

// runPackCLI implements the `session-insight pack` one-shot subcommand:
// bundle export/import without a running server, sharing the assembly logic
// with the HTTP handlers via internal/packops.
func runPackCLI(args []string, dataDir string, database *db.DB, siVersion string) error {
	if len(args) == 0 {
		packUsage()
		return errors.New("missing subcommand (export or import)")
	}
	switch args[0] {
	case "export":
		return runPackExport(args[1:], database, siVersion)
	case "import":
		return runPackImport(args[1:], dataDir, database)
	default:
		packUsage()
		return fmt.Errorf("unknown subcommand %q (want export or import)", args[0])
	}
}

func packUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  session-insight pack export -o <file.sibundle> [--include-raw] [--redact] [--case <label>] [--force] <agent_type:id> [<agent_type:id> ...]
  session-insight pack import <file.sibundle>`)
}

// parseSelection splits an "agent_type:id" argument on the FIRST colon —
// agent ids (e.g. grok's) contain '-' but never ':'.
func parseSelection(arg string) (packops.Selection, error) {
	agentType, id, ok := strings.Cut(arg, ":")
	if !ok {
		return packops.Selection{}, fmt.Errorf("invalid session selector %q: want <agent_type>:<id>", arg)
	}
	if agentType == "" || id == "" {
		return packops.Selection{}, fmt.Errorf("invalid session selector %q: agent type and id must both be non-empty", arg)
	}
	return packops.Selection{AgentType: agentType, ID: id}, nil
}

func runPackExport(args []string, database *db.DB, siVersion string) error {
	fs := flag.NewFlagSet("pack export", flag.ContinueOnError)
	out := fs.String("o", "", "output .sibundle path (required)")
	includeRaw := fs.Bool("include-raw", false, "attach raw source files captured via provenance")
	redact := fs.Bool("redact", false, "best-effort secret/home-path redaction")
	caseLabel := fs.String("case", "", "case label recorded in the bundle manifest")
	force := fs.Bool("force", false, "overwrite the output file if it exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		packUsage()
		return errors.New("export: -o <file.sibundle> is required")
	}
	pos := fs.Args()
	if len(pos) == 0 {
		packUsage()
		return errors.New("export: at least one <agent_type:id> selector is required")
	}
	sels := make([]packops.Selection, 0, len(pos))
	for _, arg := range pos {
		sel, err := parseSelection(arg)
		if err != nil {
			return err
		}
		sels = append(sels, sel)
	}

	// Live agent readers only; imported snapshots are not re-exported here.
	readers := reader.Discover()
	res, err := packops.BuildExport(readers, database, sels, packops.ExportOptions{
		IncludeRaw: *includeRaw,
		Redact:     *redact,
		CaseLabel:  *caseLabel,
		SIVersion:  siVersion,
	})
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	for _, sk := range res.Skipped {
		fmt.Fprintf(os.Stderr, "pack export: skipped %s:%s (unknown agent or unreadable session)\n", sk.AgentType, sk.ID)
	}
	if len(res.Payloads) == 0 {
		return errors.New("export: none of the requested sessions could be read")
	}

	// Write to a sibling temp file first so --force never truncates an
	// existing pack before serialization succeeds; owner-only mode because
	// bundles may hold unredacted session content.
	outPath := *out
	if _, err := os.Stat(outPath); err == nil && !*force {
		return fmt.Errorf("export: %s already exists (pass --force to overwrite)", outPath)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("export: stat %s: %w", outPath, err)
	}
	tmpPath := outPath + ".tmp-" + fmt.Sprintf("%d", os.Getpid())
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("export: create temp %s: %w", tmpPath, err)
	}
	writeErr := bundle.WriteBundle(f, res.Manifest, res.Payloads)
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("export: write bundle: %w", writeErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("export: close temp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("export: rename to %s: %w", outPath, err)
	}

	summary := fmt.Sprintf("wrote %s: %d session(s)", outPath, len(res.Payloads))
	if res.Manifest.CaseLabel != "" {
		summary += fmt.Sprintf(", case %q", res.Manifest.CaseLabel)
	}
	fmt.Println(summary)
	return nil
}

func runPackImport(args []string, dataDir string, database *db.DB) error {
	fs := flag.NewFlagSet("pack import", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		packUsage()
		return errors.New("import: exactly one <file.sibundle> argument is required")
	}
	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("import: open %s: %w", path, err)
	}
	defer f.Close()

	importRoot := filepath.Join(dataDir, "imports")
	bundleID, manifest, err := packops.ImportBundle(database, importRoot, f)
	if err != nil {
		if errors.Is(err, bundle.ErrUnsupportedVersion) || errors.Is(err, bundle.ErrInvalidBundle) {
			// Bundle validation errors carry a clear message already.
			return err
		}
		return fmt.Errorf("import: %w", err)
	}

	// A running server picks the new sessions up on its next index pass /
	// kick; the CLI is a one-shot process and sends no live notification —
	// same style as --maintain-index.
	line := fmt.Sprintf("imported %d session(s): bundle %s, origin host %q", len(manifest.Sessions), bundleID, manifest.OriginHost)
	if manifest.CaseLabel != "" {
		line += fmt.Sprintf(", case %q", manifest.CaseLabel)
	}
	fmt.Println(line)
	return nil
}

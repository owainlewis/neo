package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/owainlewis/neo/internal/session"
)

func runSessions(ctx context.Context, args []string) {
	if len(args) == 0 {
		listSessions(ctx)
		return
	}
	if args[0] == "search" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: neo sessions search <query>")
			os.Exit(2)
		}
		searchSessions(ctx, strings.Join(args[1:], " "))
		return
	}
	fmt.Fprintf(os.Stderr, "unknown sessions command: %s\n", args[0])
	fmt.Fprintln(os.Stderr, "usage: neo sessions [search <query>]")
	os.Exit(2)
}

func listSessions(ctx context.Context) {
	store := mustSessionStore()
	items, err := store.List(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Println("no saved sessions")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tMODEL\tCWD\tTITLE")
	for _, meta := range items {
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			meta.ID,
			meta.UpdatedAt.Local().Format("2006-01-02 15:04"),
			meta.Model,
			shortPath(meta.CWD),
			title,
		)
	}
	_ = w.Flush()
}

func searchSessions(ctx context.Context, query string) {
	store := mustSessionStore()
	results, warnings, err := store.Search(ctx, query)
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: skipped session %s: %v\n", warning.ID, warning.Err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "search sessions: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("no matching sessions")
		return
	}
	printSessionSearchResults(os.Stdout, results)
}

func printSessionSearchResults(out io.Writer, results []session.SearchResult) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tMODEL\tCWD\tTITLE\tMATCH")
	for _, result := range results {
		meta := result.Metadata
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			meta.ID,
			meta.UpdatedAt.Local().Format("2006-01-02 15:04"),
			meta.Model,
			shortPath(meta.CWD),
			title,
			result.Excerpt,
		)
	}
	_ = w.Flush()
}

func mustSessionStore() *session.Store {
	store, err := session.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		os.Exit(1)
	}
	return store
}

func shortPath(path string) string {
	if path == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (path == home || strings.HasPrefix(path, home+string(os.PathSeparator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

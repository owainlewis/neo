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

func runSessions(ctx context.Context, args []string, streams stdio) int {
	if len(args) == 0 {
		return listSessions(ctx, streams)
	}
	if args[0] == "search" {
		if len(args) < 2 {
			fmt.Fprintln(streams.err, "usage: neo sessions search <query>")
			return 2
		}
		return searchSessions(ctx, strings.Join(args[1:], " "), streams)
	}
	fmt.Fprintf(streams.err, "unknown sessions command: %s\n", args[0])
	fmt.Fprintln(streams.err, "usage: neo sessions [search <query>]")
	return 2
}

func listSessions(ctx context.Context, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	items, err := store.List(ctx)
	if err != nil {
		fmt.Fprintf(streams.err, "list sessions: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(streams.out, "no saved sessions")
		return 0
	}
	w := tabwriter.NewWriter(streams.out, 0, 0, 2, ' ', 0)
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
	return 0
}

func searchSessions(ctx context.Context, query string, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	results, warnings, err := store.Search(ctx, query)
	for _, warning := range warnings {
		fmt.Fprintf(streams.err, "warning: skipped session %s: %v\n", warning.ID, warning.Err)
	}
	if err != nil {
		fmt.Fprintf(streams.err, "search sessions: %v\n", err)
		return 1
	}
	if len(results) == 0 {
		fmt.Fprintln(streams.out, "no matching sessions")
		return 0
	}
	printSessionSearchResults(streams.out, results)
	return 0
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

func loadSessionStore(errOut io.Writer) (*session.Store, bool) {
	store, err := session.DefaultStore()
	if err != nil {
		fmt.Fprintf(errOut, "sessions: %v\n", err)
		return nil, false
	}
	return store, true
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

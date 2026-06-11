package factory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RenderTree is a pure function: Snapshot -> one text frame. Stdlib only,
// so it drops into any view layer with one call.
//
//	● orchestrator  work the backlog: owainlewis/app             3m12s
//	├─ ● worker     #12 invite teammate by email                 2m07s
//	│  │  bash: just test
//	│  └─ ✓ verify  PR #34 vs acceptance criteria                  31s
//	└─ ✗ worker     #13 rate limiting (timeout after 10m)       10m00s
func RenderTree(nodes []NodeView) string {
	byParent := map[int][]NodeView{}
	for _, n := range nodes {
		byParent[n.Parent] = append(byParent[n.Parent], n)
	}
	for _, kids := range byParent {
		sort.Slice(kids, func(i, j int) bool { return kids[i].ID < kids[j].ID })
	}
	var b strings.Builder
	roots := byParent[0]
	for i, root := range roots {
		render(&b, root, byParent, "", i == len(roots)-1)
	}
	return b.String()
}

func render(b *strings.Builder, n NodeView, byParent map[int][]NodeView, prefix string, last bool) {
	connector, childPrefix := "├─ ", prefix+"│  "
	if last {
		connector, childPrefix = "└─ ", prefix+"   "
	}
	if n.Parent == 0 {
		connector, childPrefix = "", ""
	}

	fmt.Fprintf(b, "%s%s%s %-12s %-44s %7s\n",
		prefix, connector, glyph(n), n.Step, clip(n.Task, 44), dur(n.Elapsed))

	// Live nodes show their latest event as a status line beneath them.
	if !n.Done && n.LastLine != "" {
		fmt.Fprintf(b, "%s│  %s\n", childPrefix, clip(n.LastLine, 70))
	}
	kids := byParent[n.ID]
	for i, k := range kids {
		render(b, k, byParent, childPrefix, i == len(kids)-1)
	}
}

func glyph(n NodeView) string {
	switch {
	case n.Err != "":
		return "✗"
	case n.Done:
		return "✓"
	default:
		return "●"
	}
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

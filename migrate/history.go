package migrate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	mg "github.com/matthewmcneely/modusgraph"
)

// HistoryEntry is one migration's place in the chain, annotated with applied
// state. Entries appear root->head when the chain is sound; otherwise they are
// ID-sorted so a broken chain still renders deterministically.
type HistoryEntry struct {
	ID      int64
	Name    string
	After   int64    // predecessor ID; 0 on the root
	Applied bool     // a full migration record exists in this database
	IsRoot  bool     // After == 0
	Steps   []string // step names, for --verbose rendering
}

// HistoryResult is the analyzed chain. Entries is always populated — even when
// the chain is broken — so the renderer can show the fault. Valid reports
// whether the chain is structurally sound; Err carries the first structural
// fault (one of the chain Err* types) when it is not.
type HistoryResult struct {
	Entries []HistoryEntry
	Valid   bool
	Err     error
}

// History analyzes the migration chain against this database. It returns a fully
// populated result and, when the chain is structurally broken, the typed chain
// error (so callers can render the fault and still exit non-zero).
func History(ctx context.Context, c mg.Client, migrations []Migration) (HistoryResult, error) {
	return history(ctx, newDgraphStore(c), migrations)
}

func history(ctx context.Context, s store, migrations []Migration) (HistoryResult, error) {
	if err := s.bootstrap(ctx); err != nil {
		return HistoryResult{}, fmt.Errorf("migrate: bootstrap: %w", err)
	}
	applied, err := s.loadAppliedMigrations(ctx)
	if err != nil {
		return HistoryResult{}, err
	}
	appliedSet := make(map[int64]bool, len(applied))
	for _, r := range applied {
		appliedSet[r.ID] = true
	}
	res := analyzeHistory(migrations, appliedSet)
	return res, res.Err
}

// analyzeHistory is the pure core: it orders and validates the chain and
// annotates each migration with applied state. It performs no I/O, so it is the
// reusable basis for both the live command and an offline chain lint.
func analyzeHistory(migrations []Migration, appliedSet map[int64]bool) HistoryResult {
	ordered, err := buildChain(migrations)
	src := ordered
	if err != nil {
		// Broken chain: buildChain returns no order, so fall back to an
		// ID sort for a stable render of the unsound set.
		src = append([]Migration(nil), migrations...)
		sort.Slice(src, func(i, j int) bool { return src[i].ID < src[j].ID })
	}
	entries := make([]HistoryEntry, 0, len(src))
	for _, m := range src {
		steps := make([]string, 0, len(m.Steps))
		for _, st := range m.Steps {
			steps = append(steps, st.Name)
		}
		entries = append(entries, HistoryEntry{
			ID:      m.ID,
			Name:    m.Name,
			After:   m.After,
			Applied: appliedSet[m.ID],
			IsRoot:  m.After == 0,
			Steps:   steps,
		})
	}
	return HistoryResult{Entries: entries, Valid: err == nil, Err: err}
}

// RenderHistory formats a HistoryResult for the terminal. With tree, it draws
// the parent/child graph (marking forks) whether or not the chain is sound;
// otherwise it prints a one-line-per-migration list for a sound chain or the
// fault block for a broken one. verbose adds each migration's step count/names.
func RenderHistory(res HistoryResult, tree, verbose bool) string {
	if tree {
		return renderTree(res, verbose)
	}
	if res.Valid {
		return renderLinear(res, verbose)
	}
	return renderBroken(res)
}

func box(applied bool) string {
	if applied {
		return "[x]"
	}
	return "[ ]"
}

func renderLinear(res HistoryResult, verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migrations (%d)  root → head\n\n", len(res.Entries))

	nameW := 0
	for _, e := range res.Entries {
		if len(e.Name) > nameW {
			nameW = len(e.Name)
		}
	}

	var head, latestApplied int64
	pending := 0
	for _, e := range res.Entries {
		clause := fmt.Sprintf("after %d", e.After)
		if e.IsRoot {
			clause = "(root)"
		}
		line := fmt.Sprintf("  %s %d  %-*s  %s", box(e.Applied), e.ID, nameW, e.Name, clause)
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteByte('\n')
		if verbose {
			b.WriteString(renderSteps("        ", e))
		}
		head = e.ID // entries are root->head, so the last is the head
		if e.Applied {
			if e.ID > latestApplied {
				latestApplied = e.ID
			}
		} else {
			pending++
		}
	}

	appliedStr := "none"
	if latestApplied != 0 {
		appliedStr = fmt.Sprintf("%d", latestApplied)
	}
	fmt.Fprintf(&b, "\nhead %d   applied %s   %d pending\n", head, appliedStr, pending)
	b.WriteString("([x] applied · [ ] pending · [~] in progress)\n")
	return b.String()
}

func renderSteps(indent string, e HistoryEntry) string {
	if len(e.Steps) == 0 {
		return indent + "(no steps)\n"
	}
	return fmt.Sprintf("%s%d step(s): %s\n", indent, len(e.Steps), strings.Join(e.Steps, ", "))
}

// renderBroken prints the fault for a structurally broken chain. Divergence
// gets the detailed predecessor/children block from the spec; every other fault
// prints its typed error message.
func renderBroken(res HistoryResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migrations (%d)  ✘ broken chain\n\n", len(res.Entries))

	var de *ErrDivergentHistory
	if errors.As(res.Err, &de) {
		nameOf := func(id int64) string {
			for _, e := range res.Entries {
				if e.ID == id {
					return e.Name
				}
			}
			return ""
		}
		fmt.Fprintf(&b, "  ✘ DIVERGENT HISTORY — predecessor %d has %d children:\n", de.After, len(de.Children))
		children := append([]int64(nil), de.Children...)
		sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
		for _, c := range children {
			fmt.Fprintf(&b, "      %d  %s\n", c, nameOf(c))
		}
		b.WriteString("    Re-point one migration's After to the other to linearize.\n")
		return b.String()
	}

	if res.Err != nil {
		fmt.Fprintf(&b, "  ✘ %s\n", res.Err.Error())
	}
	return b.String()
}

// renderTree draws the chain as a parent/child graph reconstructed from each
// entry's After link. A node with more than one child is a fork (divergence).
// Nodes unreachable from a root — a cycle or a dangling predecessor — print in a
// trailing "orphaned" section so a broken graph still renders fully.
func renderTree(res HistoryResult, verbose bool) string {
	nodes := make(map[int64]HistoryEntry, len(res.Entries))
	children := make(map[int64][]int64)
	var roots []int64
	for _, e := range res.Entries {
		nodes[e.ID] = e
		if e.IsRoot {
			roots = append(roots, e.ID)
		} else {
			children[e.After] = append(children[e.After], e.ID)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	for k := range children {
		ids := children[k]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		children[k] = ids
	}

	var b strings.Builder
	visited := make(map[int64]bool, len(res.Entries))

	var draw func(id int64, prefix string, isLast, isRoot bool)
	draw = func(id int64, prefix string, isLast, isRoot bool) {
		if visited[id] {
			return // cycle guard
		}
		visited[id] = true
		e := nodes[id]

		connector, childPrefix := "", prefix
		if !isRoot {
			if isLast {
				connector, childPrefix = "└── ", prefix+"    "
			} else {
				connector, childPrefix = "├── ", prefix+"│   "
			}
		}
		marker := ""
		if len(children[id]) > 1 {
			marker = fmt.Sprintf("        ✘ %d children — divergent", len(children[id]))
		}
		fmt.Fprintf(&b, "%s%s%s %d  %s%s\n", prefix, connector, box(e.Applied), e.ID, e.Name, marker)
		if verbose {
			b.WriteString(renderSteps(childPrefix+"  ", e))
		}
		kids := children[id]
		for i, k := range kids {
			draw(k, childPrefix, i == len(kids)-1, false)
		}
	}
	for i, r := range roots {
		draw(r, "", i == len(roots)-1, true)
	}

	var orphans []int64
	for _, e := range res.Entries {
		if !visited[e.ID] {
			orphans = append(orphans, e.ID)
		}
	}
	if len(orphans) > 0 {
		sort.Slice(orphans, func(i, j int) bool { return orphans[i] < orphans[j] })
		b.WriteString("\norphaned (unreachable from a root — cycle or missing predecessor):\n")
		for _, id := range orphans {
			e := nodes[id]
			fmt.Fprintf(&b, "  %s %d  %s  after %d\n", box(e.Applied), e.ID, e.Name, e.After)
		}
	}
	return b.String()
}

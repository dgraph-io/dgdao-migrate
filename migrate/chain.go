package migrate

// buildChain validates the migration set and returns it ordered root->head by
// following After links. It enforces: unique IDs, exactly one root (After == 0),
// every After references a registered migration, no predecessor has more than
// one child, and no cycles.
func buildChain(migrations []Migration) ([]Migration, error) {
	byID := make(map[int64]Migration, len(migrations))
	for _, m := range migrations {
		if prev, dup := byID[m.ID]; dup {
			return nil, &ErrDuplicateID{ID: m.ID, Names: [2]string{prev.Name, m.Name}}
		}
		byID[m.ID] = m
	}

	var roots []int64
	childOf := make(map[int64][]int64) // predecessor ID -> child IDs
	for _, m := range migrations {
		if m.After == 0 {
			roots = append(roots, m.ID)
			continue
		}
		if _, ok := byID[m.After]; !ok {
			return nil, &ErrUnknownPredecessor{ID: m.ID, After: m.After}
		}
		childOf[m.After] = append(childOf[m.After], m.ID)
	}
	if len(roots) == 0 {
		return nil, &ErrNoRoot{}
	}
	if len(roots) > 1 {
		return nil, &ErrMultipleRoots{IDs: roots}
	}
	for after, children := range childOf {
		if len(children) > 1 {
			return nil, &ErrDivergentHistory{After: after, Children: children}
		}
	}

	ordered := make([]Migration, 0, len(migrations))
	cur := roots[0]
	for {
		ordered = append(ordered, byID[cur])
		children := childOf[cur]
		if len(children) == 0 {
			break
		}
		cur = children[0]
	}
	if len(ordered) != len(migrations) {
		seen := make(map[int64]bool, len(ordered))
		for _, m := range ordered {
			seen[m.ID] = true
		}
		var orphans []int64
		for _, m := range migrations {
			if !seen[m.ID] {
				orphans = append(orphans, m.ID)
			}
		}
		return nil, &ErrCycle{IDs: orphans}
	}
	return ordered, nil
}

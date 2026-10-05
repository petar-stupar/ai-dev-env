package stacks

import (
	"fmt"
	"sort"
	"strings"
)

// Resolve expands requires, rejects unknown names, conflicts (in either
// direction) and cycles, and orders the result topologically with ties broken
// by Order then Name. Input order does not affect the output, and duplicate
// names in the input are ignored.
func Resolve(cat map[string]*Stack, selected []string) (*Resolution, error) {
	// Expand requires transitively.
	set := map[string]*Stack{}
	var add func(name, from string) error
	add = func(name, from string) error {
		if _, ok := set[name]; ok {
			return nil
		}
		s, ok := cat[name]
		if !ok || s == nil {
			if from != "" {
				return fmt.Errorf("stack %s requires unknown stack %q", from, name)
			}
			return fmt.Errorf("unknown stack %q (known: %s)", name, strings.Join(sortedKeys(cat), ", "))
		}
		set[name] = s
		for _, r := range s.Requires {
			if err := add(r, name); err != nil {
				return err
			}
		}
		return nil
	}
	sel := append([]string(nil), selected...)
	sort.Strings(sel)
	for _, n := range sel {
		if err := add(n, ""); err != nil {
			return nil, err
		}
	}
	names := sortedKeys(set)

	// Conflicts, either direction.
	for _, n := range names {
		for _, c := range set[n].Conflicts {
			if _, ok := set[c]; ok {
				a, b := n, c
				if b < a {
					a, b = b, a
				}
				return nil, fmt.Errorf("stacks %s and %s conflict", a, b)
			}
		}
	}

	if cyc := findCycle(set, names); cyc != nil {
		return nil, fmt.Errorf("stack requires form a cycle: %s", strings.Join(cyc, " -> "))
	}

	// Kahn's algorithm; the ready set is kept ordered by (Order, Name).
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for _, n := range names {
		seen := map[string]bool{}
		for _, r := range set[n].Requires {
			if seen[r] {
				continue
			}
			seen[r] = true
			indeg[n]++
			dependents[r] = append(dependents[r], n)
		}
	}
	less := func(a, b string) bool {
		if set[a].Order != set[b].Order {
			return set[a].Order < set[b].Order
		}
		return a < b
	}
	var ready []string
	for _, n := range names {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	res := &Resolution{}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		n := ready[0]
		ready = ready[1:]
		res.Stacks = append(res.Stacks, set[n])
		for _, d := range dependents[n] {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	if len(res.Stacks) != len(names) { // unreachable after findCycle
		return nil, fmt.Errorf("stack requires form a cycle")
	}

	for _, s := range res.Stacks {
		if len(s.Suggests) == 0 {
			continue
		}
		hit := false
		for _, g := range s.Suggests {
			if _, ok := set[g]; ok {
				hit = true
				break
			}
		}
		if !hit {
			res.Warnings = append(res.Warnings, fmt.Sprintf("stack %s suggests one of %s; none selected", s.Name, strings.Join(s.Suggests, ", ")))
		}
	}
	return res, nil
}

// findCycle returns a requires cycle as a path that starts and ends with the
// same name, or nil. Traversal is in name order so the report is stable.
func findCycle(set map[string]*Stack, names []string) []string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		color[n] = grey
		stack = append(stack, n)
		reqs := append([]string(nil), set[n].Requires...)
		sort.Strings(reqs)
		for _, r := range reqs {
			switch color[r] {
			case grey:
				for i, m := range stack {
					if m == r {
						return append(append([]string(nil), stack[i:]...), r)
					}
				}
			case white:
				if c := visit(r); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}
	for _, n := range names {
		if color[n] == white {
			if c := visit(n); c != nil {
				return c
			}
		}
	}
	return nil
}

// Run returns the union of the stacks' run requirements: CapAdd, SecurityOpt
// and Devices sorted and deduplicated; Env merged in resolution order, so when
// two stacks set the same variable the later stack wins. Empty fields are nil.
func (r *Resolution) Run() RunReq {
	var out RunReq
	for _, s := range r.Stacks {
		out.CapAdd = append(out.CapAdd, s.Run.CapAdd...)
		out.SecurityOpt = append(out.SecurityOpt, s.Run.SecurityOpt...)
		out.Devices = append(out.Devices, s.Run.Devices...)
		for k, v := range s.Run.Env {
			if out.Env == nil {
				out.Env = map[string]string{}
			}
			out.Env[k] = v
		}
	}
	out.CapAdd = sortDedup(out.CapAdd)
	out.SecurityOpt = sortDedup(out.SecurityOpt)
	out.Devices = sortDedup(out.Devices)
	return out
}

// Allow returns the hosts the stacks need, sorted and deduplicated.
func (r *Resolution) Allow() []string {
	var out []string
	for _, s := range r.Stacks {
		out = append(out, s.Allow...)
	}
	return sortDedup(out)
}

func sortDedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	s := append([]string(nil), in...)
	sort.Strings(s)
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

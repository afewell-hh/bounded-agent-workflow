package inspect

// sourceProfile is the trusted per-request list of maintained sources, in
// output order, and its membership set. Profiles are built only from fixed
// application strings; no caller supplies a path. A profile is never
// modified after construction, so concurrent requests cannot affect each
// other.
type sourceProfile struct {
	paths  []string
	member map[string]bool
}

// legacyProfile is the fixed lead profile of `baw inspect`: the shared
// SourcePaths list and its membership set, both unchanged.
func legacyProfile() sourceProfile { return sourceProfile{SourcePaths, allowlisted} }

// ContextSourcePaths returns a new copy of the eight `baw context` sources
// for role, in output order, or false unless role is exactly "lead",
// "worker" or "reviewer". Only the selected role file is included.
func ContextSourcePaths(role string) ([]string, bool) {
	switch role {
	case "lead", "worker", "reviewer":
	default:
		return nil, false
	}
	return []string{
		"AGENTS.md",
		"workflow/protocol.md",
		"workflow/roles/" + role + ".md",
		"README.md",
		"docs/design/product-contract.md",
		"docs/architecture/overview.md",
		"docs/developer/environment.md",
		"docs/operator/agent-lifecycle.md",
	}, true
}

// InspectContext performs the Inspect algorithm on repo with the context
// source profile of role substituted for the lead profile. No checkpoint or
// coordination input is accepted. An invalid role is CodeUsage before any
// filesystem or Git work.
func InspectContext(repo, role string, limits Limits) (*Packet, error) {
	paths, ok := ContextSourcePaths(role)
	if !ok {
		return nil, fail(CodeUsage)
	}
	member := make(map[string]bool, len(paths))
	for _, p := range paths {
		member[p] = true
	}
	return inspect(Options{Repo: repo, Limits: limits}, sourceProfile{paths, member})
}

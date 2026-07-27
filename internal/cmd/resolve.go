package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
)

// keyShaped reports whether ref can be tried as a client-side environment
// key: non-empty and alphanumeric (so it is safe in a URL path). Names can
// look like this too — their retrieve probe just misses.
func keyShaped(ref string) bool {
	if ref == "" {
		return false
	}
	for _, r := range ref {
		if !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
			return false
		}
	}
	return true
}

// matchByName returns the canonical values (map keys) whose name matches ref
// case-insensitively, sorted for determinism.
func matchByName(canonicalToName map[string]string, ref string) []string {
	var out []string
	for canonical, name := range canonicalToName {
		if strings.EqualFold(name, ref) {
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}

// pickCandidate resolves an ambiguous name to a single canonical value:
// one match returns directly; several are disambiguated per 05 §2 (pick in a
// TTY, usage error otherwise). idKind names the entity's canonical
// identifier — "key" for environments, "id" for everything else — so the
// error offers exactly the identifier that works.
func pickCandidate(cmd *cobra.Command, entity, idKind, ref string, candidates []string, names map[string]string) (string, error) {
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if !interactive() {
		return "", usageErrorf("%s %q is ambiguous (%d matches) — use its %s instead", entity, ref, len(candidates), idKind)
	}
	options := make([]string, len(candidates))
	for i, c := range candidates {
		options[i] = label(names[c], c)
	}
	idx, err := selectPrompt(cmd, entity, fmt.Sprintf("Multiple %ss named %q", entity, ref), options, 0)
	if err != nil {
		return "", err
	}
	return candidates[idx], nil
}

// resolveEnvironment turns the environment context reference (a client-side
// key or a name) into the full environment within a project, over the Admin
// API. Flag commands need the numeric id (features query) and the key
// (update-flag mutations), so both come from one lookup.
func resolveEnvironment(cmd *cobra.Command, pc *projectContext, cred *activeCredential, projectID int) (api.Environment, error) {
	ref, _ := pc.Environment.Value.(string)
	if ref == "" {
		ref = os.Getenv("FLAGSMITH_ENVIRONMENT_KEY")
	}
	if ref == "" {
		return api.Environment{}, withHint(errors.New("no environment"),
			"Pass -e, set FLAGSMITH_ENVIRONMENT, or run `flagsmith init`.")
	}

	// The common post-init ref is an exact client-side key, which the
	// retrieve endpoint serves in one targeted call — much cheaper than the
	// project's full environment list. Names can be key-shaped too; their
	// probe just 404s and resolution falls back to the list below. A key
	// belonging to another project also falls through, so the error names
	// the project the context asked for.
	if keyShaped(ref) {
		e, err := cred.client().GetEnvironment(cmd.Context(), ref)
		switch {
		case err == nil && e.Project == projectID:
			_ = cache.Merge(apiURL, &cache.Names{Environments: map[string]string{e.APIKey: e.Name}})
			return *e, nil
		case err != nil && !api.IsNotFound(err):
			return api.Environment{}, err
		}
	}

	envs, err := cred.client().Environments(cmd.Context(), projectID)
	if err != nil {
		return api.Environment{}, err
	}
	byKey := make(map[string]string, len(envs))
	for _, e := range envs {
		byKey[e.APIKey] = e.Name
	}
	_ = cache.Merge(apiURL, &cache.Names{Environments: byKey})

	for _, e := range envs {
		if e.APIKey == ref {
			return e, nil
		}
	}
	hits := matchByName(byKey, ref)
	if len(hits) == 0 {
		return api.Environment{}, withHint(
			fmt.Errorf("environment %q not found in project %d", ref, projectID),
			hintEnvironmentList)
	}
	chosen, err := pickCandidate(cmd, "environment", "key", ref, hits, byKey)
	if err != nil {
		return api.Environment{}, err
	}
	for _, e := range envs {
		if e.APIKey == chosen {
			return e, nil
		}
	}
	return api.Environment{}, withHint(
		fmt.Errorf("environment %q not found in project %d", ref, projectID),
		hintEnvironmentList)
}

// resolveProjectID turns the project reference (an id or a name) into an id.
func resolveProjectID(cmd *cobra.Command, pc *projectContext, cred *activeCredential) (int, error) {
	if id, ok := pc.Project.Value.(int); ok {
		return id, nil
	}
	name, ok := pc.Project.Value.(string)
	if !ok || name == "" {
		return 0, withHint(errors.New("no project"),
			"Set --project, or `project` in flagsmith.json.")
	}
	orgID, err := resolveOrganisationID(cmd, pc, cred)
	if err != nil {
		return 0, err
	}
	projects, err := cred.client().Projects(cmd.Context(), orgID)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]string, len(projects))
	for _, p := range projects {
		byID[strconv.Itoa(p.ID)] = p.Name
	}
	_ = cache.Merge(apiURL, &cache.Names{Projects: byID}) // opportunistic (04 §3)
	hits := matchByName(byID, name)
	if len(hits) == 0 {
		return 0, withHint(
			fmt.Errorf("project %q not found in organisation %d", name, orgID),
			hintProjectList)
	}
	chosen, err := pickCandidate(cmd, "project", "id", name, hits, byID)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(chosen)
}

// resolveOrganisationID turns the organisation reference (an id or a name)
// into an id, defaulting to the sole organisation when unset.
func resolveOrganisationID(cmd *cobra.Command, pc *projectContext, cred *activeCredential) (int, error) {
	if id, ok := pc.Organisation.Value.(int); ok {
		return id, nil
	}
	orgs, err := cred.client().Organisations(cmd.Context())
	if err != nil {
		return 0, err
	}
	rememberOrganisations(orgs)
	if name, ok := pc.Organisation.Value.(string); ok && name != "" {
		byID := make(map[string]string, len(orgs))
		for _, o := range orgs {
			byID[strconv.Itoa(o.ID)] = o.Name
		}
		hits := matchByName(byID, name)
		if len(hits) == 0 {
			return 0, withHint(
				fmt.Errorf("organisation %q not found", name),
				hintOrganisationList)
		}
		chosen, err := pickCandidate(cmd, "organisation", "id", name, hits, byID)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(chosen)
	}
	if len(orgs) == 1 {
		return orgs[0].ID, nil
	}
	return 0, withHint(errors.New("multiple organisations are accessible with these credentials"),
		"Set --organisation to resolve a project name.")
}

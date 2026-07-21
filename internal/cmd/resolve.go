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
// TTY, usage error otherwise).
func pickCandidate(cmd *cobra.Command, entity, ref string, candidates []string, names map[string]string) (string, error) {
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if !interactive() {
		return "", usageErrorf("%s %q is ambiguous (%d matches) — use its id/key instead", entity, ref, len(candidates))
	}
	options := make([]string, len(candidates))
	for i, c := range candidates {
		options[i] = fmt.Sprintf("%s (%s)", names[c], c)
	}
	idx, err := selectPrompt(cmd, entity, fmt.Sprintf("Multiple %ss named %q", entity, ref), options, 0)
	if err != nil {
		return "", err
	}
	return candidates[idx], nil
}

// resolveEnvironmentKey turns the environment context reference (a key or a
// name) into a client-side key for SDK reads. FLAGSMITH_ENVIRONMENT_KEY is
// the SDK credential and is always a key.
func resolveEnvironmentKey(cmd *cobra.Command, pc *projectContext) (string, error) {
	if v := os.Getenv("FLAGSMITH_ENVIRONMENT_KEY"); v != "" {
		return v, nil
	}
	ref, _ := pc.Environment.Value.(string)
	if ref == "" {
		return "", errors.New(
			"no environment — set FLAGSMITH_ENVIRONMENT_KEY, pass -e, or run `flagsmith init`")
	}

	// A known key is used directly — keys are globally unique. Environment
	// *names*, however, are unique only within a project, so the flat
	// instance cache cannot resolve them; that goes through the project-scoped
	// Admin list below.
	if _, ok := cache.Load(apiURL).Environments[ref]; ok {
		return ref, nil
	}

	// Resolve over the Admin API when credentials allow; otherwise assume the
	// reference already is a key (credless path — the SDK validates it).
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return ref, nil
	}
	projectID, err := resolveProjectID(cmd, pc, cred)
	if err != nil {
		return "", err
	}
	envs, err := api.Environments(cmd.Context(), apiURL, cred.auth, projectID)
	if err != nil {
		return "", err
	}
	byKey := make(map[string]string, len(envs))
	for _, e := range envs {
		byKey[e.APIKey] = e.Name
	}
	_ = cache.Merge(apiURL, &cache.Names{Environments: byKey})

	if _, ok := byKey[ref]; ok {
		return ref, nil
	}
	if hits := matchByName(byKey, ref); len(hits) > 0 {
		return pickCandidate(cmd, "environment", ref, hits, byKey)
	}
	return "", fmt.Errorf("environment %q not found in project %d", ref, projectID)
}

// resolveProjectID turns the project reference (an id or a name) into an id.
func resolveProjectID(cmd *cobra.Command, pc *projectContext, cred *activeCredential) (int, error) {
	if id, ok := pc.Project.Value.(int); ok {
		return id, nil
	}
	name, ok := pc.Project.Value.(string)
	if !ok || name == "" {
		return 0, errors.New("no project — set --project or `project` in flagsmith.json")
	}
	orgID, err := resolveOrganisationID(cmd, pc, cred)
	if err != nil {
		return 0, err
	}
	projects, err := api.Projects(cmd.Context(), apiURL, cred.auth, orgID)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]string, len(projects))
	for _, p := range projects {
		byID[strconv.Itoa(p.ID)] = p.Name
	}
	hits := matchByName(byID, name)
	if len(hits) == 0 {
		return 0, fmt.Errorf("project %q not found in organisation %d", name, orgID)
	}
	chosen, err := pickCandidate(cmd, "project", name, hits, byID)
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
	orgs, err := api.Organisations(cmd.Context(), apiURL, cred.auth)
	if err != nil {
		return 0, err
	}
	if name, ok := pc.Organisation.Value.(string); ok && name != "" {
		byID := make(map[string]string, len(orgs))
		for _, o := range orgs {
			byID[strconv.Itoa(o.ID)] = o.Name
		}
		hits := matchByName(byID, name)
		if len(hits) == 0 {
			return 0, fmt.Errorf("organisation %q not found", name)
		}
		chosen, err := pickCandidate(cmd, "organisation", name, hits, byID)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(chosen)
	}
	if len(orgs) == 1 {
		return orgs[0].ID, nil
	}
	return 0, errors.New("multiple organisations — set --organisation to resolve a project name")
}

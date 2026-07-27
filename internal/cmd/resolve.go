package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
)

// idNameMap builds the canonical→name map that ref resolution and the name
// cache work on, from any fetched slice.
func idNameMap[T any](items []T, entry func(T) (canonical, name string)) map[string]string {
	m := make(map[string]string, len(items))
	for _, it := range items {
		k, v := entry(it)
		m[k] = v
	}
	return m
}

// resolveIDRef resolves a name reference to a numeric id against a fetched
// id→name map: no match errors with the hint, several disambiguate via
// pickCandidate (05 §2).
func resolveIDRef(cmd *cobra.Command, entity, ref string, byID map[string]string, notFound error, hint string) (int, error) {
	hits := matchByName(byID, ref)
	if len(hits) == 0 {
		return 0, withHint(notFound, hint)
	}
	chosen, err := pickCandidate(cmd, entity, "id", ref, hits, byID)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(chosen)
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
		// The SDK credential doubles as an environment reference; it is
		// host-scoped to the SDK surface like everywhere else it is read.
		sdkURL, _ := pc.SDKAPIURL.Value.(string)
		_, ref = envCredential(envEnvironmentKey, sdkURL, defaultSDKAPIURL)
		// That variable is exactly where server-side keys belong (they are
		// secrets), but one can never resolve an environment over the Admin
		// API — name the variable, never its value, which must stay out of
		// stderr and CI logs.
		if strings.HasPrefix(ref, "ser.") {
			return api.Environment{}, withHint(
				errors.New("FLAGSMITH_ENVIRONMENT_KEY holds a server-side key, which cannot identify an environment for Admin commands"),
				"Pass -e, set FLAGSMITH_ENVIRONMENT, or run `flagsmith init`.")
		}
	}
	if ref == "" {
		return api.Environment{}, withHint(errors.New("no environment"),
			"Pass -e, set FLAGSMITH_ENVIRONMENT, or run `flagsmith init`.")
	}
	e, err := resolveEnvironmentRef(cmd, cred, projectID, ref)
	if err != nil {
		return api.Environment{}, err
	}
	return *e, nil
}

// resolveEnvironmentRef turns an environment reference (an api_key or a name)
// into its full record, scoped to the project, seeding the name cache on the
// way. Environments are canonically addressed by key, so an exact key match
// wins before name matching.
func resolveEnvironmentRef(cmd *cobra.Command, cred *activeCredential, projectID int, ref string) (*api.Environment, error) {
	// A server-side key can never match — the environments list carries
	// client-side keys only. Refuse before fetching, keeping the secret out
	// of the not-found message every other miss gets.
	if strings.HasPrefix(ref, "ser.") {
		return nil, withHint(
			errors.New("the environment reference takes a client-side key, not a server-side one"),
			hintServerSideKey)
	}
	envs, err := cred.client().Environments(cmd.Context(), projectID)
	if err != nil {
		return nil, err
	}
	byKey := idNameMap(envs, func(e api.Environment) (string, string) { return e.APIKey, e.Name })
	_ = cache.Merge(apiURL, &cache.Names{Environments: byKey}) // opportunistic (04 §3)

	find := func(key string) *api.Environment {
		for i := range envs {
			if envs[i].APIKey == key {
				return &envs[i]
			}
		}
		return nil
	}
	if e := find(ref); e != nil {
		return e, nil
	}
	notFound := withHint(
		fmt.Errorf("environment %q not found in project %d", ref, projectID),
		hintEnvironmentList)
	hits := matchByName(byKey, ref)
	if len(hits) == 0 {
		return nil, notFound
	}
	chosen, err := pickCandidate(cmd, "environment", "key", ref, hits, byKey)
	if err != nil {
		return nil, err
	}
	if e := find(chosen); e != nil {
		return e, nil
	}
	return nil, notFound
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
	byID := idNameMap(projects, func(p api.Project) (string, string) { return strconv.Itoa(p.ID), p.Name })
	_ = cache.Merge(apiURL, &cache.Names{Projects: byID}) // opportunistic (04 §3)
	return resolveIDRef(cmd, "project", name, byID,
		fmt.Errorf("project %q not found in organisation %d", name, orgID),
		hintProjectList)
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
		byID := idNameMap(orgs, func(o api.Organisation) (string, string) { return strconv.Itoa(o.ID), o.Name })
		return resolveIDRef(cmd, "organisation", name, byID,
			fmt.Errorf("organisation %q not found", name),
			hintOrganisationList)
	}
	if len(orgs) == 1 {
		return orgs[0].ID, nil
	}
	return 0, withHint(errors.New("multiple organisations are accessible with these credentials"),
		"Set --organisation to resolve a project name.")
}

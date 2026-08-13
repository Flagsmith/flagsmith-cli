package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/cache"
)

// idNameMap builds a canonical→name map from a fetched slice.
func idNameMap[T any](items []T, entry func(T) (canonical, name string)) map[string]string {
	m := make(map[string]string, len(items))
	for _, it := range items {
		k, v := entry(it)
		m[k] = v
	}
	return m
}

// resolveIDRef resolves a name reference to a numeric id against a fetched
// id→name map: no match errors with the hint, several go to pickCandidate.
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

// pickCandidate resolves an ambiguous name to a single canonical value: one
// match returns directly; several prompt in a TTY and are a usage error
// otherwise. idKind names the entity's canonical identifier — "key" for
// environments, "id" for everything else — so the error offers the identifier
// that actually works.
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

// resolveEnvironment turns the environment context reference (a client-side key
// or a name) into the full environment within a project, over the Admin API.
func resolveEnvironment(cmd *cobra.Command, pc *projectContext, cred *activeCredential, projectID int) (api.Environment, error) {
	ref, _ := pc.Environment.Value.(string)
	if ref == "" {
		// The SDK credential doubles as an environment reference, host-scoped
		// to the SDK surface.
		sdkURL, _ := pc.SDKAPIURL.Value.(string)
		name, r := envCredential(envEnvironmentKey, sdkURL, defaultSDKAPIURL)
		ref = r
		// Server-side keys belong in that variable but can never resolve an
		// environment over the Admin API. Name the variable, never its value:
		// it is a secret and must stay out of stderr and CI logs.
		if strings.HasPrefix(ref, "ser.") {
			return api.Environment{}, withHint(
				fmt.Errorf("%s holds a server-side key, which cannot identify an environment for Admin commands", name),
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

// environmentKeyShape matches a client-side SDK key: the backend mints them as
// shortuuids, which are always 22 alphanumerics. It tells a key apart from a
// name, so a key nothing has cached yet still reaches the SDK API without an
// Admin credential. A 22-character name with no separators is the false
// positive, and even that resolves once the cache has seen the environment.
var environmentKeyShape = regexp.MustCompile(`^[0-9A-Za-z]{22}$`)

// sdkEnvironmentKey returns the environment key the SDK API authenticates with.
//
// FLAGSMITH_ENVIRONMENT_KEY wins: it is the only home for a server-side key, and
// it names its environment outright. Otherwise the environment context is a key
// or a name, as it is everywhere else — and a name is turned into a key by the
// local name cache wherever it can be, so evaluation stays free of the Admin API
// and of any credential for it. Falling through to the Admin API is what makes a
// name work at all on a cold cache; it is also the only way to scope a name that
// the instance-wide cache holds more than one of.
func sdkEnvironmentKey(cmd *cobra.Command, pc *projectContext) (string, error) {
	sdkURL, _ := pc.SDKAPIURL.Value.(string)
	if _, v := envCredential(envEnvironmentKey, sdkURL, defaultSDKAPIURL); v != "" {
		return v, nil
	}
	ref, _ := pc.Environment.Value.(string)
	if ref == "" {
		return "", hintf(errors.New("no environment key"),
			"Set %s, or pass -e.", environmentKeyVar())
	}
	names := cache.Load(pc.apiURL()).Environments
	if _, cached := names[ref]; cached {
		return ref, nil
	}
	if hits := matchByName(names, ref); len(hits) == 1 {
		return hits[0], nil
	}
	if environmentKeyShape.MatchString(ref) {
		return ref, nil
	}
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return "", err
	}
	projectID, err := resolveProjectID(cmd, pc, cred)
	if err != nil {
		return "", err
	}
	env, err := resolveEnvironmentRef(cmd, cred, projectID, ref)
	if err != nil {
		return "", err
	}
	return env.APIKey, nil
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
			hintServerSideKey())
	}
	envs, err := cred.client().Environments(cmd.Context(), projectID)
	if err != nil {
		return nil, err
	}
	byKey := idNameMap(envs, func(e api.Environment) (string, string) { return e.APIKey, e.Name })
	_ = cache.Merge(apiURL, &cache.Names{Environments: byKey}) // opportunistic

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
	_ = cache.Merge(apiURL, &cache.Names{Projects: byID}) // opportunistic
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

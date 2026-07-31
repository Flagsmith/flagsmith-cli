package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/config"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
)

// schemaURL pins the $schema written by init to the schema of the writing
// CLI: a release references its own tag; any other build (dev, pseudo-
// version) references main, which always exists — a branch or revision URL
// would 404 once its ref is gone.
func schemaURL() string {
	ref := "main"
	if version.IsRelease(version.Version) {
		ref = version.Version
	}
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/%s/schema/flagsmith.json", ref)
}

var (
	createProjectFlag     string
	createEnvironmentFlag string
)

var initCmd = &cobra.Command{
	Use:         "init",
	Short:       "Bind this directory to a Flagsmith project (writes flagsmith.json)",
	Annotations: map[string]string{annotationLongRunning: "true"},
	Example: `  # interactive: log in, pick a project and environment
  flagsmith init

  # non-interactive: name the project, create a new environment
  flagsmith init --project acme-api --create-environment Staging`,
	RunE: runInit,
}

// explicitValue returns a context value only when it came from a flag or
// env var — a decision, as opposed to a flagsmith.json value which is a
// default the prompts pre-select.
func explicitValue(r resolved) (any, bool) {
	if r.Value != nil && (r.Source == sourceCLI || r.Source == sourceEnv) {
		return r.Value, true
	}
	return nil, false
}

// resolveOrganisation determines the organisation to act in and seeds the
// name cache. The bool is true when more than one organisation was
// available — the choice is then worth recording in flagsmith.json. An
// explicit --organisation wins; a lone org is used silently; otherwise the
// picker runs, self-guarding to a usage error naming --organisation without a TTY.
func resolveOrganisation(cmd *cobra.Command, pc *projectContext, cred *activeCredential, names *cache.Names) (api.Organisation, bool, error) {
	orgs, err := cred.client().Organisations(cmd.Context())
	if err != nil {
		return api.Organisation{}, false, err
	}
	for _, o := range orgs {
		names.Organisations[strconv.Itoa(o.ID)] = o.Name
	}
	if len(orgs) == 0 {
		return api.Organisation{}, false, withHint(
			errors.New("no organisations are accessible with these credentials"),
			hintWrongAccount)
	}
	ambiguous := len(orgs) > 1
	if v, ok := explicitValue(pc.Organisation); ok {
		want := v.(int)
		for _, o := range orgs {
			if o.ID == want {
				return o, ambiguous, nil
			}
		}
		return api.Organisation{}, false, withHint(
			fmt.Errorf("organisation %d is not accessible with these credentials", want),
			hintWrongAccount)
	}
	if len(orgs) == 1 {
		return orgs[0], false, nil
	}
	configOrg := 0
	if pc.Organisation.Source == sourceConfig {
		configOrg, _ = pc.Organisation.Value.(int)
	}
	options := make([]string, len(orgs))
	defaultOrg := 0
	for i, o := range orgs {
		options[i] = label(o.Name, o.ID)
		if o.ID == configOrg {
			defaultOrg = i
		}
	}
	idx, err := selectPrompt(cmd, "organisation", "Organisation", options, defaultOrg)
	if err != nil {
		return api.Organisation{}, false, err
	}
	return orgs[idx], true, nil
}

func runInit(cmd *cobra.Command, args []string) error {
	pc, err := applyContext(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	// Creating a resource and selecting an existing one are mutually exclusive.
	if createProjectFlag != "" {
		if _, ok := explicitValue(pc.Project); ok {
			return usageErrorf("--project and --create-project are mutually exclusive")
		}
	}
	if createEnvironmentFlag != "" {
		if _, ok := explicitValue(pc.Environment); ok {
			return usageErrorf("--environment and --create-environment are mutually exclusive")
		}
	}

	// Credentials: log in inline when interactive, fail with the ways out
	// otherwise.
	cred, err := resolveCredential(ctx)
	if errors.Is(err, auth.ErrNotLoggedIn) {
		if !interactive() {
			return withHint(errors.New("no credentials found, and a browser login needs a TTY"),
				"Set FLAGSMITH_API_KEY, run in a CI OIDC context with an org trust relationship, or run `flagsmith login` interactively first.")
		}
		if err := browserLogin(cmd); err != nil {
			return err
		}
		if cred, err = resolveCredential(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// A project/organisation passed by name is resolved to its canonical ID
	// up front, so the rest of init works in IDs and records them.
	if _, ok := pc.Organisation.Value.(string); ok {
		id, err := resolveOrganisationID(cmd, pc, cred)
		if err != nil {
			return err
		}
		pc.Organisation.Value = id
	}
	if _, ok := pc.Project.Value.(string); ok {
		id, err := resolveProjectID(cmd, pc, cred)
		if err != nil {
			return err
		}
		pc.Project.Value = id
	}

	names := &cache.Names{
		Organisations: map[string]string{},
		Projects:      map[string]string{},
		Environments:  map[string]string{},
	}

	// Carry forward any organisation already in context (existing file, flag,
	// or env) so re-init never drops it; a multi-org choice overrides it.
	organisationID := 0
	if v, ok := pc.Organisation.Value.(int); ok {
		organisationID = v
	}

	projectID := 0
	if v, ok := explicitValue(pc.Project); ok {
		projectID = v.(int)
	}

	switch {
	case createProjectFlag != "":
		org, ambiguous, err := resolveOrganisation(cmd, pc, cred, names)
		if err != nil {
			return err
		}
		if ambiguous {
			organisationID = org.ID
		}
		created, err := cred.client().CreateProject(ctx, map[string]any{"name": createProjectFlag, "organisation": org.ID})
		if err != nil {
			return fmt.Errorf("creating project: %w", err)
		}
		output.Success(errOut, "Created project %s", label(created.Name, created.ID))
		projectID = created.ID
		names.Projects[strconv.Itoa(created.ID)] = created.Name
	case projectID != 0:
		// use the explicitly provided project
	default:
		if !interactive() {
			return withHint(
				usageErrorf("no TTY and no --project/--create-project given"),
				"Non-interactively: flagsmith init --project <id> | --create-project <name> [--environment <key>] --yes")
		}
		org, ambiguous, err := resolveOrganisation(cmd, pc, cred, names)
		if err != nil {
			return err
		}
		if ambiguous {
			organisationID = org.ID
		}
		projects, err := cred.client().Projects(ctx, org.ID)
		if err != nil {
			return err
		}
		configProjectID := 0
		if pc.Project.Source == sourceConfig {
			configProjectID, _ = pc.Project.Value.(int)
		}
		options := make([]string, 0, len(projects)+1)
		defaultProject := 0
		for i, p := range projects {
			options = append(options, label(p.Name, p.ID))
			if p.ID == configProjectID {
				defaultProject = i
			}
		}
		options = append(options, "Create a new project")
		idx, err := selectPrompt(cmd, "project", "Project", options, defaultProject)
		if err != nil {
			return err
		}
		if idx == len(projects) {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			name, err := textPrompt(cmd, "create-project", "Project name", filepath.Base(cwd))
			if err != nil {
				return err
			}
			created, err := cred.client().CreateProject(ctx, map[string]any{"name": name, "organisation": org.ID})
			if err != nil {
				return fmt.Errorf("creating project: %w", err)
			}
			output.Success(errOut, "Created project %s", label(created.Name, created.ID))
			projectID = created.ID
			names.Projects[strconv.Itoa(created.ID)] = created.Name
		} else {
			projectID = projects[idx].ID
			names.Projects[strconv.Itoa(projectID)] = projects[idx].Name
		}
	}

	// Environments: doubles as the access check.
	envs, err := cred.client().Environments(ctx, projectID)
	if err != nil {
		return fmt.Errorf("verifying access to project %d: %w", projectID, err)
	}
	for _, e := range envs {
		names.Environments[e.APIKey] = e.Name
	}
	if !interactive() && createProjectFlag == "" && createEnvironmentFlag == "" {
		output.Success(errOut, "Verified access to project %d", projectID)
	}

	envKey := ""
	if v, ok := explicitValue(pc.Environment); ok {
		envKey = v.(string)
	}
	configEnvKey := ""
	if pc.Environment.Source == sourceConfig {
		configEnvKey, _ = pc.Environment.Value.(string)
	}
	switch {
	case createEnvironmentFlag != "":
		created, err := cred.client().CreateEnvironment(ctx, map[string]any{"name": createEnvironmentFlag, "project": projectID})
		if err != nil {
			return fmt.Errorf("creating environment: %w", err)
		}
		output.Success(errOut, "Created environment %s", created.Name)
		envKey = created.APIKey
		names.Environments[created.APIKey] = created.Name
	case envKey != "":
		found := false
		for _, e := range envs {
			if e.APIKey == envKey {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("environment key %q not found in project %d", envKey, projectID)
		}
	case interactive() && len(envs) == 0:
		// A project with no environments (e.g. one just created) can't be
		// picked from — offer to create one, defaulting to Development.
		name, err := textPrompt(cmd, "create-environment", "No environments yet — create one named", "Development")
		if err != nil {
			return err
		}
		created, err := cred.client().CreateEnvironment(ctx, map[string]any{"name": name, "project": projectID})
		if err != nil {
			return fmt.Errorf("creating environment: %w", err)
		}
		output.Success(errOut, "Created environment %s", created.Name)
		envKey = created.APIKey
		names.Environments[created.APIKey] = created.Name
	case interactive() && len(envs) > 0:
		options := make([]string, 0, len(envs)+1)
		def := 0
		for i, e := range envs {
			options = append(options, label(e.Name, e.APIKey))
			if strings.EqualFold(e.Name, "Development") && configEnvKey == "" {
				def = i
			}
			if e.APIKey == configEnvKey {
				def = i
			}
		}
		options = append(options, "(skip)")
		idx, err := selectPrompt(cmd, "environment", "Default environment", options, def)
		if err != nil {
			return err
		}
		if idx < len(envs) {
			envKey = envs[idx].APIKey
		}
	default:
		// Non-interactive with no environment flag: carry forward the
		// existing config value rather than dropping it.
		envKey = configEnvKey
	}

	newFile := &config.File{
		Schema:       schemaURL(),
		Project:      refOrNil(projectID),
		Organisation: refOrNil(organisationID),
		Environment:  envKey,
	}
	if pc.APIURL.Value.(string) != defaultAPIURL {
		newFile.APIURL = pc.APIURL.Value.(string)
	}
	// Persist a non-default SDK endpoint. A default source means the value is
	// either Edge or re-derived from apiUrl on load, so it needn't be written;
	// anything set explicitly (flag, env, or the existing file) is carried
	// forward so re-init never silently drops a custom SDK endpoint.
	if pc.SDKAPIURL.Source != sourceDefault {
		newFile.SDKAPIURL = pc.SDKAPIURL.Value.(string)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, config.FileName)
	if _, err := os.Stat(target); err == nil {
		old, _, loadErr := config.Load(target)
		if loadErr != nil {
			// A file we can't parse might hold hand-edited settings; refuse to
			// clobber it rather than replacing it with a fresh one.
			return withHint(fmt.Errorf(
				"%s already exists but could not be parsed, refusing to overwrite it: %w",
				config.FileName, loadErr),
				"Fix or remove the file, then re-run `flagsmith init`.")
		}
		// Preserve any fields this CLI doesn't recognise across the rewrite.
		newFile.Extra = old.Extra
		if interactive() && !yesFlag {
			fmt.Fprintf(out, "%s exists — updating it.\n\n%s\n", config.FileName, fileDiff(old, newFile))
		}
		if ok, err := confirmed(cmd, "Write changes?", "written"); !ok || err != nil {
			return err
		}
	}

	if err := newFile.Save(target); err != nil {
		return err
	}
	_ = cache.Merge(apiURL, names)

	output.Success(errOut, "Wrote %s", config.FileName)
	fmt.Fprintln(errOut, "You're all set! Try:\n  flagsmith flag list")
	return nil
}

// refOrNil wraps a resolved ID as a config reference, or nil when unset so
// the field is omitted from flagsmith.json.
func refOrNil(id int) *config.Ref {
	if id == 0 {
		return nil
	}
	return &config.Ref{ID: id}
}

// fileDiff renders changed fields between two configs as -/+ JSON lines.
func fileDiff(old, updated *config.File) string {
	render := func(f *config.File) map[string]string {
		lines := map[string]string{}
		set := func(key string, v any) {
			encoded, _ := json.Marshal(v)
			lines[key] = fmt.Sprintf("%q: %s,", key, encoded)
		}
		if f.Project != nil {
			set("project", f.Project)
		}
		if f.Organisation != nil {
			set("organisation", f.Organisation)
		}
		if f.Environment != "" {
			set("environment", f.Environment)
		}
		if f.APIURL != "" {
			set("apiUrl", f.APIURL)
		}
		if f.SDKAPIURL != "" {
			set("sdkApiUrl", f.SDKAPIURL)
		}
		return lines
	}
	oldLines, newLines := render(old), render(updated)
	var b strings.Builder
	for _, key := range []string{"project", "organisation", "environment", "apiUrl", "sdkApiUrl"} {
		if oldLines[key] == newLines[key] {
			continue
		}
		if oldLines[key] != "" {
			fmt.Fprintf(&b, "- %s\n", oldLines[key])
		}
		if newLines[key] != "" {
			fmt.Fprintf(&b, "+ %s\n", newLines[key])
		}
	}
	return b.String()
}

func init() {
	initCmd.Flags().StringVar(&createProjectFlag, "create-project", "",
		"create a new project with this name and bind to it")
	initCmd.Flags().StringVar(&createEnvironmentFlag, "create-environment", "",
		"create a new environment with this name and use it")
	rootCmd.AddCommand(initCmd)
}

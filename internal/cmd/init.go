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

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
)

// version is the CLI version tag, stamped by the release build; the
// $schema URL written by init pins the schema of the writing CLI.
var version = "main"

func schemaURL() string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/Flagsmith/flagsmith-cli/%s/schema/flagsmith.json", version)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bind this directory to a Flagsmith project (writes flagsmith.json)",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	pc, err := applyContext(cmd)
	if err != nil {
		return err
	}
	initPrompts(cmd)
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	// Credentials: log in inline when interactive, fail with the ways out
	// otherwise.
	cred, err := resolveCredential(ctx)
	if errors.Is(err, auth.ErrNotLoggedIn) {
		if !interactive() {
			return errors.New(
				"no credentials found, and a browser login needs a TTY.\nSet FLAGSMITH_API_KEY, run in a CI OIDC context with an org trust relationship, or run `flagsmith login` interactively first")
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

	names := &cache.Names{
		Organisations: map[string]string{},
		Projects:      map[string]string{},
		Environments:  map[string]string{},
	}

	// Only flag/env inputs are decisions; values from an existing
	// flagsmith.json are prompt defaults — init's job is rewriting them.
	explicit := func(r resolved) (any, bool) {
		if r.Source == sourceCLI || r.Source == sourceEnv {
			return r.Value, true
		}
		return nil, false
	}

	projectID := 0
	if v, ok := explicit(pc.Project); ok {
		projectID = v.(int)
	}
	configProjectID := 0
	if pc.Project.Source == sourceConfig {
		configProjectID, _ = pc.Project.Value.(int)
	}
	organisationID := 0
	if projectID == 0 {
		if !interactive() {
			return usageErrorf(
				"no TTY and no --project given — cannot prompt.\nUsage: flagsmith init --project <id> [--environment <key>] [--api-url <url>] --yes")
		}
		orgs, err := api.Organisations(ctx, apiURL, cred.auth)
		if err != nil {
			return err
		}
		for _, o := range orgs {
			names.Organisations[strconv.Itoa(o.ID)] = o.Name
		}
		var org api.Organisation
		switch {
		case len(orgs) == 0:
			return errors.New("no organisations are accessible with these credentials")
		case pc.Organisation.Value != nil:
			wanted := pc.Organisation.Value.(int)
			for _, o := range orgs {
				if o.ID == wanted {
					org = o
				}
			}
			if org.ID == 0 {
				return fmt.Errorf("organisation %d is not accessible with these credentials", wanted)
			}
		case len(orgs) == 1:
			org = orgs[0]
		default:
			options := make([]string, len(orgs))
			for i, o := range orgs {
				options[i] = fmt.Sprintf("%s (%d)", o.Name, o.ID)
			}
			idx, err := selectPrompt(cmd, "Organisation", options, 0)
			if err != nil {
				return err
			}
			org = orgs[idx]
			organisationID = org.ID // record the choice for multi-org users
		}

		projects, err := api.Projects(ctx, apiURL, cred.auth, org.ID)
		if err != nil {
			return err
		}
		options := make([]string, 0, len(projects)+1)
		for _, p := range projects {
			options = append(options, fmt.Sprintf("%s (%d)", p.Name, p.ID))
		}
		options = append(options, "Create a new project")
		defaultProject := 0
		for i, p := range projects {
			if p.ID == configProjectID {
				defaultProject = i
			}
		}
		idx, err := selectPrompt(cmd, "Project", options, defaultProject)
		if err != nil {
			return err
		}
		if idx == len(projects) {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			name, err := textPrompt(cmd, "Project name", filepath.Base(cwd))
			if err != nil {
				return err
			}
			created, err := api.CreateProject(ctx, apiURL, cred.auth, name, org.ID)
			if err != nil {
				return fmt.Errorf("creating project: %w", err)
			}
			fmt.Fprintf(out, "✓ Created project %s (%d)\n", created.Name, created.ID)
			projectID = created.ID
			names.Projects[strconv.Itoa(created.ID)] = created.Name
		} else {
			projectID = projects[idx].ID
			names.Projects[strconv.Itoa(projectID)] = projects[idx].Name
		}
	}

	// Environments: doubles as the access check.
	envs, err := api.Environments(ctx, apiURL, cred.auth, projectID)
	if err != nil {
		return fmt.Errorf("verifying access to project %d: %w", projectID, err)
	}
	for _, e := range envs {
		names.Environments[e.APIKey] = e.Name
	}
	if !interactive() {
		fmt.Fprintf(out, "✓ Verified access to project %d\n", projectID)
	}

	envKey := ""
	if v, ok := explicit(pc.Environment); ok {
		envKey, _ = v.(string)
	}
	configEnvKey := ""
	if pc.Environment.Source == sourceConfig {
		configEnvKey, _ = pc.Environment.Value.(string)
	}
	if envKey != "" {
		found := false
		for _, e := range envs {
			if e.APIKey == envKey {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("environment key %q not found in project %d", envKey, projectID)
		}
	} else if interactive() && len(envs) > 0 {
		options := make([]string, 0, len(envs)+1)
		def := 0
		for i, e := range envs {
			options = append(options, fmt.Sprintf("%s (%s)", e.Name, e.APIKey))
			if strings.EqualFold(e.Name, "Development") && configEnvKey == "" {
				def = i
			}
			if e.APIKey == configEnvKey {
				def = i
			}
		}
		options = append(options, "(skip)")
		idx, err := selectPrompt(cmd, "Default environment", options, def)
		if err != nil {
			return err
		}
		if idx < len(envs) {
			envKey = envs[idx].APIKey
		}
	}

	newFile := &config.File{
		Schema:       schemaURL(),
		Project:      projectID,
		Organisation: organisationID,
		Environment:  envKey,
	}
	if pc.APIURL.Value.(string) != defaultAPIURL {
		newFile.APIURL = pc.APIURL.Value.(string)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, config.FileName)
	if _, err := os.Stat(target); err == nil {
		old, _, loadErr := config.Load(target)
		if loadErr != nil {
			old = &config.File{}
		}
		if interactive() {
			fmt.Fprintf(out, "%s exists — updating it.\n\n%s\n", config.FileName, fileDiff(old, newFile))
			ok, err := confirmPrompt(cmd, "Write changes?")
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(out, "Aborted; nothing written.")
				return nil
			}
		} else if !yesFlag {
			return fmt.Errorf("%s exists. Pass --yes to overwrite it non-interactively", config.FileName)
		}
	}

	encoded, err := json.MarshalIndent(newFile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	_ = cache.Merge(apiURL, names)

	fmt.Fprintf(out, "✓ Wrote %s\nYou're all set! Try:\n  flagsmith flags list\n", config.FileName)
	return nil
}

// fileDiff renders changed fields between two configs as -/+ JSON lines.
func fileDiff(old, updated *config.File) string {
	render := func(f *config.File) map[string]string {
		lines := map[string]string{}
		set := func(key string, v any) {
			encoded, _ := json.Marshal(v)
			lines[key] = fmt.Sprintf("%q: %s,", key, encoded)
		}
		if f.Project != 0 {
			set("project", f.Project)
		}
		if f.Organisation != 0 {
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
	rootCmd.AddCommand(initCmd)
}

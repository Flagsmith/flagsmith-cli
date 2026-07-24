package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
)

const (
	defaultAPIURL    = "https://api.flagsmith.com"
	defaultSDKAPIURL = "https://edge.api.flagsmith.com"

	sourceCLI     = "cli"
	sourceEnv     = "env"
	sourceConfig  = "config"
	sourceDefault = "default"
)

// resolved is one context value with its provenance and optional cached
// display name.
type resolved struct {
	Value  any    `json:"value"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source"`
}

// projectContext is the fully resolved invocation context: flag → env →
// config file → default, per value.
type projectContext struct {
	ConfigPath   resolved
	Project      resolved
	Organisation resolved
	Environment  resolved
	APIURL       resolved
	SDKAPIURL    resolved
	Warnings     []string
}

func (p *projectContext) apiURL() string {
	return p.APIURL.Value.(string)
}

// parseRef classifies a project/organisation reference string: an all-digit
// value is its numeric ID, anything else a name (resolved later via the Admin
// API). Empty yields nil.
func parseRef(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	return raw
}

// resolveContext applies the context precedence for every value and
// annotates names from the local cache. It performs no network calls and
// needs no credentials.
func resolveContext(cmd *cobra.Command) (*projectContext, error) {
	pc := &projectContext{}

	// Config file: explicit path (flag/env) wins over discovery.
	var file *config.File
	configPath := ""
	switch {
	case cmd.Flags().Changed("config-path"):
		configPath = configPathFlag
		pc.ConfigPath = resolved{Value: configPath, Source: sourceCLI}
	case os.Getenv("FLAGSMITH_CONFIG_PATH") != "":
		configPath = os.Getenv("FLAGSMITH_CONFIG_PATH")
		pc.ConfigPath = resolved{Value: configPath, Source: sourceEnv}
	default:
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		discovered, err := config.Discover(cwd)
		if err != nil {
			return nil, err
		}
		if discovered != "" {
			configPath = discovered
			pc.ConfigPath = resolved{Value: discovered, Source: sourceDefault}
		} else {
			pc.ConfigPath = resolved{Value: nil, Source: sourceDefault}
		}
	}
	if configPath != "" {
		loaded, warnings, err := config.Load(configPath)
		if err != nil {
			return nil, err
		}
		file = loaded
		pc.Warnings = warnings
	} else {
		file = &config.File{}
	}

	// project (an ID or a name; see parseRef)
	switch {
	case cmd.Flags().Changed("project"):
		pc.Project = resolved{Value: parseRef(projectFlag), Source: sourceCLI}
	case os.Getenv("FLAGSMITH_PROJECT") != "":
		pc.Project = resolved{Value: parseRef(os.Getenv("FLAGSMITH_PROJECT")), Source: sourceEnv}
	case file.Project != nil:
		pc.Project = resolved{Value: file.Project.Value(), Source: sourceConfig}
	default:
		pc.Project = resolved{Value: nil, Source: sourceDefault}
	}

	// organisation (an ID or a name; see parseRef)
	switch {
	case cmd.Flags().Changed("organisation"):
		pc.Organisation = resolved{Value: parseRef(organisationFlag), Source: sourceCLI}
	case os.Getenv("FLAGSMITH_ORGANISATION") != "":
		pc.Organisation = resolved{Value: parseRef(os.Getenv("FLAGSMITH_ORGANISATION")), Source: sourceEnv}
	case file.Organisation != nil:
		pc.Organisation = resolved{Value: file.Organisation.Value(), Source: sourceConfig}
	default:
		pc.Organisation = resolved{Value: nil, Source: sourceDefault}
	}

	// environment (client-side key; ser.* never belongs in context)
	switch {
	case cmd.Flags().Changed("environment"):
		pc.Environment = resolved{Value: environmentFlag, Source: sourceCLI}
	case os.Getenv("FLAGSMITH_ENVIRONMENT") != "":
		pc.Environment = resolved{Value: os.Getenv("FLAGSMITH_ENVIRONMENT"), Source: sourceEnv}
	case file.Environment != "":
		pc.Environment = resolved{Value: file.Environment, Source: sourceConfig}
	default:
		pc.Environment = resolved{Value: nil, Source: sourceDefault}
	}
	if key, ok := pc.Environment.Value.(string); ok && strings.HasPrefix(key, "ser.") {
		return nil, withHint(
			errors.New("the environment context takes a client-side key"),
			hintServerSideKey)
	}

	// apiUrl
	switch {
	case cmd.Flags().Changed("api-url"):
		pc.APIURL = resolved{Value: strings.TrimRight(apiURLFlag, "/"), Source: sourceCLI}
	case os.Getenv("FLAGSMITH_API_URL") != "":
		pc.APIURL = resolved{Value: strings.TrimRight(os.Getenv("FLAGSMITH_API_URL"), "/"), Source: sourceEnv}
	case file.APIURL != "":
		pc.APIURL = resolved{Value: strings.TrimRight(file.APIURL, "/"), Source: sourceConfig}
	default:
		pc.APIURL = resolved{Value: defaultAPIURL, Source: sourceDefault}
	}

	// sdkApiUrl: explicit, else follows a non-default apiUrl, else Edge
	switch {
	case cmd.Flags().Changed("sdk-api-url"):
		pc.SDKAPIURL = resolved{Value: strings.TrimRight(sdkAPIURLFlag, "/"), Source: sourceCLI}
	case os.Getenv("FLAGSMITH_SDK_API_URL") != "":
		pc.SDKAPIURL = resolved{Value: strings.TrimRight(os.Getenv("FLAGSMITH_SDK_API_URL"), "/"), Source: sourceEnv}
	case file.SDKAPIURL != "":
		pc.SDKAPIURL = resolved{Value: strings.TrimRight(file.SDKAPIURL, "/"), Source: sourceConfig}
	case pc.APIURL.Source != sourceDefault:
		pc.SDKAPIURL = resolved{Value: pc.APIURL.Value, Source: sourceDefault}
	default:
		pc.SDKAPIURL = resolved{Value: defaultSDKAPIURL, Source: sourceDefault}
	}

	// Cosmetic name enrichment from the local cache — never the network.
	names := cache.Load(pc.apiURL())
	if id, ok := pc.Project.Value.(int); ok {
		pc.Project.Name = names.Projects[strconv.Itoa(id)]
	}
	if id, ok := pc.Organisation.Value.(int); ok {
		pc.Organisation.Name = names.Organisations[strconv.Itoa(id)]
	}
	if key, ok := pc.Environment.Value.(string); ok {
		pc.Environment.Name = names.Environments[key]
	}
	return pc, nil
}

// applyContext resolves the invocation context, surfaces config warnings,
// and points the auth layer at the resolved instance.
func applyContext(cmd *cobra.Command) (*projectContext, error) {
	pc, err := resolveContext(cmd)
	if err != nil {
		return nil, err
	}
	for _, w := range pc.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", w)
	}
	apiURL = pc.apiURL()
	return pc, nil
}

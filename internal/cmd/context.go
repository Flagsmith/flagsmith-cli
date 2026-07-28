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

// contextValue applies the flag → env → config → default precedence for one
// context value. transform normalises the raw flag/env string; fileVal
// reports the config file's (already typed and normalised) value, when set;
// def is the final fallback.
func contextValue(cmd *cobra.Command, flagName, flagVal, envVar string,
	transform func(string) any, fileVal func() (any, bool), def any) resolved {
	switch {
	case cmd.Flags().Changed(flagName):
		return resolved{Value: transform(flagVal), Source: sourceCLI}
	case os.Getenv(envVar) != "":
		return resolved{Value: transform(os.Getenv(envVar)), Source: sourceEnv}
	}
	if v, ok := fileVal(); ok {
		return resolved{Value: v, Source: sourceConfig}
	}
	return resolved{Value: def, Source: sourceDefault}
}

// asString and trimSlash are the context-value transforms: environments pass
// through verbatim, URLs lose their trailing slash.
func asString(raw string) any  { return raw }
func trimSlash(raw string) any { return strings.TrimRight(raw, "/") }

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

	// project and organisation (an ID or a name; see parseRef)
	pc.Project = contextValue(cmd, "project", projectFlag, "FLAGSMITH_PROJECT", parseRef,
		func() (any, bool) {
			if file.Project == nil {
				return nil, false
			}
			return file.Project.Value(), true
		}, nil)
	pc.Organisation = contextValue(cmd, "organisation", organisationFlag, "FLAGSMITH_ORGANISATION", parseRef,
		func() (any, bool) {
			if file.Organisation == nil {
				return nil, false
			}
			return file.Organisation.Value(), true
		}, nil)

	// environment (client-side key; ser.* never belongs in context)
	pc.Environment = contextValue(cmd, "environment", environmentFlag, "FLAGSMITH_ENVIRONMENT", asString,
		func() (any, bool) { return file.Environment, file.Environment != "" }, nil)
	if key, ok := pc.Environment.Value.(string); ok && strings.HasPrefix(key, "ser.") {
		return nil, withHint(
			errors.New("the environment context takes a client-side key"),
			hintServerSideKey)
	}

	pc.APIURL = contextValue(cmd, "api-url", apiURLFlag, "FLAGSMITH_API_URL", trimSlash,
		func() (any, bool) { return strings.TrimRight(file.APIURL, "/"), file.APIURL != "" }, defaultAPIURL)

	// sdkApiUrl: explicit, else follows a non-default apiUrl, else Edge
	sdkDefault := any(defaultSDKAPIURL)
	if pc.APIURL.Source != sourceDefault {
		sdkDefault = pc.APIURL.Value
	}
	pc.SDKAPIURL = contextValue(cmd, "sdk-api-url", sdkAPIURLFlag, "FLAGSMITH_SDK_API_URL", trimSlash,
		func() (any, bool) { return strings.TrimRight(file.SDKAPIURL, "/"), file.SDKAPIURL != "" }, sdkDefault)

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

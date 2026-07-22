package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// identityFlagView is the curated shape for a flag's state for one identity.
type identityFlagView struct {
	Feature    string `json:"feature"`
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Enabled    bool   `json:"enabled"`
	Value      any    `json:"value"`
}

// useEdgeIdentities reports whether the project's identity overrides live on
// the edge (DynamoDB) endpoints rather than the core ones.
func useEdgeIdentities(cmd *cobra.Command, cred *activeCredential, projectID int) (bool, error) {
	project, err := api.GetProject(cmd.Context(), apiURL, cred.auth, projectID)
	if err != nil {
		return false, err
	}
	return project.UseEdgeIdentities, nil
}

// readIdentityOverride returns an identity's override for a feature, or nil
// when the identity or the override does not exist.
func readIdentityOverride(cmd *cobra.Command, cred *activeCredential, envKey string, featureID int, identifier string, edge bool) (*api.IdentityFeatureState, error) {
	if edge {
		uuid, found, err := api.EdgeIdentityUUID(cmd.Context(), apiURL, cred.auth, envKey, identifier)
		if err != nil || !found {
			return nil, err
		}
		return api.EdgeIdentityOverride(cmd.Context(), apiURL, cred.auth, envKey, uuid, featureID)
	}
	id, found, err := api.IdentityByIdentifier(cmd.Context(), apiURL, cred.auth, envKey, identifier)
	if err != nil || !found {
		return nil, err
	}
	return api.IdentityOverride(cmd.Context(), apiURL, cred.auth, envKey, id, featureID)
}

// nativeScalar converts a typed value into the native scalar the identity
// endpoints expect (they infer the type from the value, unlike update-flag-v2).
func nativeScalar(v api.FeatureValue) (any, error) {
	switch v.Type {
	case "boolean":
		return v.Value == "true", nil
	case "integer":
		return strconv.Atoi(v.Value)
	default:
		return v.Value, nil
	}
}

// displayNative renders a native value for a confirmation line: strings
// quoted, other types bare.
func displayNative(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(v)
}

func renderIdentityDetail(cmd *cobra.Command, feature *api.Feature, identifier string, fs *api.IdentityFeatureState) error {
	v := identityFlagView{
		Feature:    feature.Name,
		Type:       featureTypeLabel(feature.Type),
		Identifier: identifier,
		Enabled:    fs.Enabled,
		Value:      fs.Value,
	}
	return output.Render(cmd.OutOrStdout(), v, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Feature", Value: v.Feature},
			{Label: "Type", Value: v.Type},
			{Label: "Identifier", Value: v.Identifier},
			{Label: "State", Value: boolState(v.Enabled)},
			{Label: "Value", Value: valueDisplay(v.Value)},
		})
	})
}

// runIdentityUpdate sets an identity override, reading the current state to
// fill the half the user did not change (a new override inherits the
// environment default value). It branches core vs edge on use_edge_identities.
func runIdentityUpdate(cmd *cobra.Command, cred *activeCredential, env api.Environment, projectID int, feature *api.Feature, identifier string, enable, disable, setValue bool) error {
	edge, err := useEdgeIdentities(cmd, cred, projectID)
	if err != nil {
		return err
	}
	current, err := readIdentityOverride(cmd, cred, env.APIKey, feature.ID, identifier, edge)
	if err != nil {
		return err
	}

	enabled := false
	if current != nil {
		enabled = current.Enabled
	}
	if enable {
		enabled = true
	}
	if disable {
		enabled = false
	}

	var value any
	if current != nil {
		value = current.Value
	} else {
		value = currentScalar(feature.EnvironmentState) // inherit the environment default
	}
	if setValue {
		fv, err := inferFeatureValue(flagValueFlag, flagTypeFlag)
		if err != nil {
			return err
		}
		if value, err = nativeScalar(fv); err != nil {
			return err
		}
	}

	scope := fmt.Sprintf("identifier %s in environment %s", identifier, environmentLabel(env))
	errOut := cmd.ErrOrStderr()
	if ok, err := confirmOrYes(cmd, fmt.Sprintf("Update %s for %s?", feature.Name, scope)); err != nil {
		return err
	} else if !ok {
		fmt.Fprintln(errOut, "Aborted; nothing changed.")
		return nil
	}

	if edge {
		err = api.SetEdgeIdentityOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, identifier, feature.ID, enabled, value)
	} else {
		id, found, ferr := api.IdentityByIdentifier(cmd.Context(), apiURL, cred.auth, env.APIKey, identifier)
		if ferr != nil {
			return ferr
		}
		if !found {
			if id, err = api.CreateIdentity(cmd.Context(), apiURL, cred.auth, env.APIKey, identifier); err != nil {
				return err
			}
		}
		fsID := 0
		if current != nil {
			fsID = current.ID
		}
		err = api.SetIdentityOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, id, feature.ID, fsID, enabled, value)
	}
	if err != nil {
		return err
	}

	if setValue {
		output.Success(errOut, "Set %s to %s for %s", feature.Name, displayNative(value), scope)
	}
	if enable {
		output.Success(errOut, "Enabled %s for %s", feature.Name, scope)
	}
	if disable {
		output.Success(errOut, "Disabled %s for %s", feature.Name, scope)
	}

	updated, err := readIdentityOverride(cmd, cred, env.APIKey, feature.ID, identifier, edge)
	if err != nil || updated == nil {
		return err
	}
	return renderIdentityDetail(cmd, feature, identifier, updated)
}

// runIdentityDelete removes an identity override, branching core vs edge.
func runIdentityDelete(cmd *cobra.Command, cred *activeCredential, env api.Environment, projectID int, name, identifier string) error {
	features, err := api.Features(cmd.Context(), apiURL, cred.auth, projectID, env.ID, 0)
	if err != nil {
		return err
	}
	feature := findFeature(features, name)
	if feature == nil {
		return fmt.Errorf("feature %q not found in %s", name, environmentLabel(env))
	}
	edge, err := useEdgeIdentities(cmd, cred, projectID)
	if err != nil {
		return err
	}

	scope := fmt.Sprintf("identifier %s in environment %s", identifier, environmentLabel(env))
	errOut := cmd.ErrOrStderr()
	if ok, err := confirmOrYes(cmd, fmt.Sprintf("Delete %s override for %s?", name, scope)); err != nil {
		return err
	} else if !ok {
		fmt.Fprintln(errOut, "Aborted; nothing changed.")
		return nil
	}

	if edge {
		err = api.DeleteEdgeIdentityOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, identifier, feature.ID)
	} else {
		id, found, ferr := api.IdentityByIdentifier(cmd.Context(), apiURL, cred.auth, env.APIKey, identifier)
		if ferr != nil {
			return ferr
		}
		if !found {
			return fmt.Errorf("identity %q not found in %s", identifier, environmentLabel(env))
		}
		override, oerr := api.IdentityOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, id, feature.ID)
		if oerr != nil {
			return oerr
		}
		if override == nil {
			return fmt.Errorf("%q has no override for identifier %q in %s", name, identifier, environmentLabel(env))
		}
		err = api.DeleteIdentityOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, id, override.ID)
	}
	if err != nil {
		return err
	}
	output.Success(errOut, "Deleted %s override for %s", name, scope)
	return nil
}

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var featureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Manage features in the current project",
}

var featureIncludeArchived bool

// variantView / featureView are the curated output shapes.
type variantView struct {
	ID     int     `json:"id"`
	Value  any     `json:"value"`
	Weight float64 `json:"weight"`
	Key    string  `json:"key,omitempty"`
}

type featureView struct {
	ID          int           `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Type        string        `json:"type"`
	Value       any           `json:"default_value"`
	Enabled     bool          `json:"enabled"`
	Variants    []variantView `json:"variants,omitempty"`
}

// mvOptionValue extracts a multivariate option's typed value as a scalar.
func mvOptionValue(o api.MultivariateOption) any {
	switch o.Type {
	case "int":
		if o.IntegerValue != nil {
			return *o.IntegerValue
		}
	case "bool":
		if o.BooleanValue != nil {
			return *o.BooleanValue
		}
	default:
		if o.StringValue != nil {
			return *o.StringValue
		}
	}
	return nil
}

func toFeatureView(f *api.Feature) featureView {
	v := featureView{
		ID: f.ID, Name: f.Name, Description: f.Description,
		Type: featureTypeLabel(f.Type), Enabled: f.DefaultEnabled,
	}
	if f.InitialValue != nil {
		v.Value = *f.InitialValue
	}
	for _, o := range f.MultivariateOptions {
		v.Variants = append(v.Variants, variantView{
			ID: o.ID, Value: mvOptionValue(o), Weight: weightOf(o), Key: o.Key,
		})
	}
	return v
}

func weightOf(o api.MultivariateOption) float64 {
	if o.DefaultPercentageAllocation != nil {
		return *o.DefaultPercentageAllocation
	}
	return 0
}

func formatWeight(w float64) string {
	return strconv.FormatFloat(w, 'f', -1, 64)
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features in the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		features, err := api.ProjectFeatures(cmd.Context(), apiURL, cred.auth, projectID, featureIncludeArchived)
		if err != nil {
			return err
		}
		views := make([]featureView, len(features))
		for i := range features {
			views[i] = toFeatureView(&features[i])
		}
		return output.Render(cmd.OutOrStdout(), views, outputOpts(), func(w io.Writer) error {
			if len(views) == 0 {
				fmt.Fprintln(w, "No features.")
				return nil
			}
			rows := make([][]string, len(views))
			for i, v := range views {
				rows[i] = []string{v.Name, strconv.Itoa(v.ID), v.Type, truncateValue(valueDisplay(v.Value)), v.Description}
			}
			if err := output.Table(w, []string{"NAME", "ID", "TYPE", "DEFAULT VALUE", "DESCRIPTION"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(views), plural(len(views), "feature", "features"))
			return nil
		})
	},
}

var featureGetCmd = &cobra.Command{
	Use:   "get <feature>",
	Short: "Show a feature and its variants",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		feat, err := api.GetFeature(cmd.Context(), apiURL, cred.auth, projectID, id)
		if err != nil {
			return err
		}
		return renderFeature(cmd, feat)
	},
}

// renderFeature prints a feature's detail view + variants (human) or the
// curated JSON.
func renderFeature(cmd *cobra.Command, f *api.Feature) error {
	view := toFeatureView(f)
	return output.Render(cmd.OutOrStdout(), view, outputOpts(), func(w io.Writer) error {
		if err := output.Detail(w, []output.Field{
			{Label: "Feature", Value: fmt.Sprintf("%s (%d)", f.Name, f.ID)},
			{Label: "Description", Value: f.Description},
			{Label: "Type", Value: view.Type},
			{Label: "Default value", Value: valueDisplay(view.Value)},
			{Label: "Enabled", Value: strconv.FormatBool(view.Enabled)},
		}); err != nil {
			return err
		}
		if len(view.Variants) > 0 {
			fmt.Fprintln(w, "\nVariants")
			var buf bytes.Buffer
			tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "VALUE\tWEIGHT\tKEY\tID")
			for _, v := range view.Variants {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", fmt.Sprint(v.Value), formatWeight(v.Weight), v.Key, v.ID)
			}
			tw.Flush()
			for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		return nil
	})
}

var (
	featureValueFlag       string
	featureEnabledFlag     bool
	featureDescriptionFlag string
	featureVariantsFlag    string
	featureArchiveFlag     bool
	featureUnarchiveFlag   bool
)

var featureCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		in := api.FeatureWrite{Name: args[0]}
		if cmd.Flags().Changed("value") || cmd.Flags().Changed("default-value") {
			in.InitialValue = &featureValueFlag
		}
		if cmd.Flags().Changed("description") {
			in.Description = &featureDescriptionFlag
		}
		if cmd.Flags().Changed("enabled") {
			in.DefaultEnabled = &featureEnabledFlag
		}
		if cmd.Flags().Changed("variants") {
			if in.MultivariateOptions, err = variantsForWrite(cmd, featureVariantsFlag); err != nil {
				return err
			}
		}
		feat, err := api.CreateFeature(cmd.Context(), apiURL, cred.auth, projectID, in)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created feature %s (%d)", feat.Name, feat.ID)
		return renderFeature(cmd, feat)
	},
}

var featureUpdateCmd = &cobra.Command{
	Use:   "update <feature>",
	Short: "Update a feature's description or archive state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if featureArchiveFlag && featureUnarchiveFlag {
			return usageErrorf("--archive and --unarchive are mutually exclusive")
		}
		in := api.FeatureWrite{}
		changed := false
		if cmd.Flags().Changed("description") {
			in.Description = &featureDescriptionFlag
			changed = true
		}
		if featureArchiveFlag {
			t := true
			in.IsArchived = &t
			changed = true
		}
		if featureUnarchiveFlag {
			fl := false
			in.IsArchived = &fl
			changed = true
		}
		if !changed {
			return usageErrorf("nothing to update — pass --description, --archive, or --unarchive")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		feat, err := api.UpdateFeature(cmd.Context(), apiURL, cred.auth, projectID, id, in)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated feature %s (%d)", feat.Name, feat.ID)
		return renderFeature(cmd, feat)
	},
}

var featureDeleteCmd = &cobra.Command{
	Use:   "delete <feature>",
	Short: "Delete a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete feature %s (%d)", args[0], id)); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := api.DeleteFeature(cmd.Context(), apiURL, cred.auth, projectID, id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted feature %s (%d)", args[0], id)
		return nil
	},
}

// variantInput is one entry of the inline --variants JSON array.
type variantInput struct {
	Value  any     `json:"value"`
	Weight float64 `json:"weight"`
}

// mvOptionFromJSON types a variant from its JSON value: bool → boolean,
// number → integer, else string.
func mvOptionFromJSON(value any, weight float64) api.MultivariateOption {
	w := weight
	o := api.MultivariateOption{DefaultPercentageAllocation: &w}
	switch t := value.(type) {
	case bool:
		v := t
		o.Type, o.BooleanValue = "bool", &v
	case float64:
		n := int(t)
		o.Type, o.IntegerValue = "int", &n
	default:
		s := fmt.Sprint(value)
		o.Type, o.StringValue = "unicode", &s
	}
	return o
}

func variantsForWrite(cmd *cobra.Command, arg string) ([]api.MultivariateOption, error) {
	raw, err := readRuleArg(cmd, arg)
	if err != nil {
		return nil, err
	}
	var inputs []variantInput
	if err := json.Unmarshal(raw, &inputs); err != nil {
		return nil, fmt.Errorf("parsing --variants: %w", err)
	}
	opts := make([]api.MultivariateOption, len(inputs))
	for i, in := range inputs {
		opts[i] = mvOptionFromJSON(in.Value, in.Weight)
	}
	return opts, nil
}

var (
	featureVariantValueFlag  string
	featureVariantWeightFlag float64
	featureVariantKeyFlag    string
	featureVariantTypeFlag   string
)

var featureVariantCmd = &cobra.Command{
	Use:   "variant",
	Short: "Manage a multivariate feature's variants",
}

// mvOptionFromFlag types a variant from a --value string, honouring --type.
func mvOptionFromFlag(value, typeFlag string) (api.MultivariateOption, error) {
	fv, err := inferFeatureValue(value, typeFlag)
	if err != nil {
		return api.MultivariateOption{}, err
	}
	o := api.MultivariateOption{}
	switch fv.Type {
	case "boolean":
		b := fv.Value == "true"
		o.Type, o.BooleanValue = "bool", &b
	case "integer":
		n, _ := strconv.Atoi(fv.Value)
		o.Type, o.IntegerValue = "int", &n
	default:
		s := fv.Value
		o.Type, o.StringValue = "unicode", &s
	}
	return o, nil
}

// findVariant resolves a variant reference (id or key) on a feature.
func findVariant(f *api.Feature, ref string) *api.MultivariateOption {
	if id, err := strconv.Atoi(ref); err == nil {
		for i := range f.MultivariateOptions {
			if f.MultivariateOptions[i].ID == id {
				return &f.MultivariateOptions[i]
			}
		}
	}
	for i := range f.MultivariateOptions {
		if f.MultivariateOptions[i].Key == ref {
			return &f.MultivariateOptions[i]
		}
	}
	return nil
}

func variantLabel(o *api.MultivariateOption) string {
	return fmt.Sprint(mvOptionValue(*o))
}

var featureVariantListCmd = &cobra.Command{
	Use:   "list <feature>",
	Short: "List a feature's variants",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		feat, err := api.GetFeature(cmd.Context(), apiURL, cred.auth, projectID, id)
		if err != nil {
			return err
		}
		variants := toFeatureView(feat).Variants
		return output.Render(cmd.OutOrStdout(), variants, outputOpts(), func(w io.Writer) error {
			if len(variants) == 0 {
				fmt.Fprintln(w, "No variants.")
				return nil
			}
			rows := make([][]string, len(variants))
			for i, v := range variants {
				rows[i] = []string{fmt.Sprint(v.Value), formatWeight(v.Weight), v.Key, strconv.Itoa(v.ID)}
			}
			return output.Table(w, []string{"VALUE", "WEIGHT", "KEY", "ID"}, rows)
		})
	},
}

var featureVariantAddCmd = &cobra.Command{
	Use:   "add <feature>",
	Short: "Add a variant to a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("value") {
			return usageErrorf("a variant needs a value — pass --value")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		o, err := mvOptionFromFlag(featureVariantValueFlag, featureVariantTypeFlag)
		if err != nil {
			return err
		}
		o.Feature = id
		if cmd.Flags().Changed("weight") {
			w := featureVariantWeightFlag
			o.DefaultPercentageAllocation = &w
		}
		if cmd.Flags().Changed("key") {
			o.Key = featureVariantKeyFlag
		}
		created, err := api.CreateMVOption(cmd.Context(), apiURL, cred.auth, projectID, id, o)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Added variant %s (%d) to %s", variantLabel(created), created.ID, args[0])
		return nil
	},
}

var featureVariantUpdateCmd = &cobra.Command{
	Use:   "update <feature> <variant>",
	Short: "Update a variant (by id or key)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("value") && !cmd.Flags().Changed("weight") && !cmd.Flags().Changed("key") {
			return usageErrorf("nothing to update — pass --value, --weight, or --key")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		feat, err := api.GetFeature(cmd.Context(), apiURL, cred.auth, projectID, id)
		if err != nil {
			return err
		}
		variant := findVariant(feat, args[1])
		if variant == nil {
			return fmt.Errorf("variant %q not found on %s", args[1], args[0])
		}
		o := api.MultivariateOption{}
		if cmd.Flags().Changed("value") {
			if o, err = mvOptionFromFlag(featureVariantValueFlag, featureVariantTypeFlag); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("weight") {
			w := featureVariantWeightFlag
			o.DefaultPercentageAllocation = &w
		}
		if cmd.Flags().Changed("key") {
			o.Key = featureVariantKeyFlag
		}
		if _, err := api.UpdateMVOption(cmd.Context(), apiURL, cred.auth, projectID, id, variant.ID, o); err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated variant %s (%d)", variantLabel(variant), variant.ID)
		return nil
	},
}

var featureVariantDeleteCmd = &cobra.Command{
	Use:   "delete <feature> <variant>",
	Short: "Delete a variant (by id or key)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveFeatureID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		feat, err := api.GetFeature(cmd.Context(), apiURL, cred.auth, projectID, id)
		if err != nil {
			return err
		}
		variant := findVariant(feat, args[1])
		if variant == nil {
			return fmt.Errorf("variant %q not found on %s", args[1], args[0])
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete variant %s (%d) from %s", variantLabel(variant), variant.ID, args[0])); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := api.DeleteMVOption(cmd.Context(), apiURL, cred.auth, projectID, id, variant.ID); err != nil {
			return err
		}
		output.Success(errOut, "Deleted variant %s (%d) from %s", variantLabel(variant), variant.ID, args[0])
		return nil
	},
}

func init() {
	featureListCmd.Flags().BoolVar(&featureIncludeArchived, "include-archived", false, "include archived features")
	featureCreateCmd.Flags().StringVar(&featureValueFlag, "value", "", "the feature's default value")
	featureCreateCmd.Flags().StringVar(&featureValueFlag, "default-value", "", "alias for --value")
	_ = featureCreateCmd.Flags().MarkHidden("default-value")
	featureCreateCmd.Flags().BoolVar(&featureEnabledFlag, "enabled", false, "enable the feature by default")
	featureCreateCmd.Flags().StringVar(&featureDescriptionFlag, "description", "", "feature description")
	featureCreateCmd.Flags().StringVar(&featureVariantsFlag, "variants", "", "multivariate variants: @file, -, or inline JSON")
	featureUpdateCmd.Flags().StringVar(&featureDescriptionFlag, "description", "", "feature description")
	featureUpdateCmd.Flags().BoolVar(&featureArchiveFlag, "archive", false, "archive the feature")
	featureUpdateCmd.Flags().BoolVar(&featureUnarchiveFlag, "unarchive", false, "unarchive the feature")
	for _, c := range []*cobra.Command{featureVariantAddCmd, featureVariantUpdateCmd} {
		c.Flags().StringVar(&featureVariantValueFlag, "value", "", "variant value")
		c.Flags().Float64Var(&featureVariantWeightFlag, "weight", 0, "variant weight (percentage allocation)")
		c.Flags().StringVar(&featureVariantKeyFlag, "key", "", "variant key")
		c.Flags().StringVar(&featureVariantTypeFlag, "type", "", "force the value type: string, integer, or boolean")
	}
	featureVariantCmd.AddCommand(featureVariantListCmd, featureVariantAddCmd, featureVariantUpdateCmd, featureVariantDeleteCmd)
	featureCmd.AddCommand(featureListCmd, featureGetCmd, featureCreateCmd, featureUpdateCmd, featureDeleteCmd, featureVariantCmd)
	rootCmd.AddCommand(featureCmd)
}

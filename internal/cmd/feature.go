package cmd

import (
	"bytes"
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
	Value       any           `json:"value"`
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
			ID: o.ID, Value: mvOptionValue(o), Weight: o.DefaultPercentageAllocation, Key: o.Key,
		})
	}
	return v
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
				rows[i] = []string{v.Name, strconv.Itoa(v.ID), v.Type, valueDisplay(v.Value), v.Description}
			}
			if err := output.Table(w, []string{"NAME", "ID", "TYPE", "VALUE", "DESCRIPTION"}, rows); err != nil {
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
			{Label: "Value", Value: valueDisplay(view.Value)},
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

func init() {
	featureListCmd.Flags().BoolVar(&featureIncludeArchived, "include-archived", false, "include archived features")
	featureCmd.AddCommand(featureListCmd, featureGetCmd)
	rootCmd.AddCommand(featureCmd)
}

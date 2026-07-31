package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
)

// segmentRuleSchema is stamped onto emitted rules so a saved rule file
// validates against Flagsmith's published evaluation-context schema.
const segmentRuleSchema = "https://raw.githubusercontent.com/Flagsmith/flagsmith/main/sdk/evaluation-context.json#/$defs/SegmentRule"

var segmentCmd = &cobra.Command{
	Use:   "segment",
	Short: "Manage segments in the current project",
}

var (
	segmentIncludeFeatureSpecific bool
	segmentRulesFlag              string
	segmentDescriptionFlag        string
	segmentFeatureFlag            string
)

// --- curated output shapes ---

type conditionView struct {
	Property string `json:"property,omitempty"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type ruleView struct {
	Schema     string          `json:"$schema,omitempty"`
	Type       string          `json:"type"`
	Conditions []conditionView `json:"conditions,omitempty"`
	Rules      []ruleView      `json:"rules,omitempty"`
}

type segmentView struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Feature     *int      `json:"feature,omitempty"`
	Rules       *ruleView `json:"rules"`
}

// projectScopedContext resolves the credential and project a project-scoped command
// needs (segments are project-scoped; no environment).
func projectScopedContext(cmd *cobra.Command) (*activeCredential, int, error) {
	pc, err := applyContext(cmd)
	if err != nil {
		return nil, 0, err
	}
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return nil, 0, err
	}
	projectID, err := resolveProjectID(cmd, pc, cred)
	if err != nil {
		return nil, 0, err
	}
	return cred, projectID, nil
}

var segmentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List segments in the current project",
	Example: `  flagsmith segment list

  # include feature-specific segments
  flagsmith segment list --include-feature-specific`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		segs, err := cred.client().Segments(cmd.Context(), projectID, segmentIncludeFeatureSpecific)
		if err != nil {
			return err
		}
		views := make([]segmentView, len(segs))
		for i := range segs {
			views[i] = toSegmentView(&segs[i])
		}
		// The condition count comes from the raw rules; the row joins back to
		// segs by index (views mirror it in order).
		return renderList(cmd, views, "No segments.",
			[]string{"NAME", "ID", "CONDITIONS", "DESCRIPTION"},
			func(i int, _ segmentView) []string {
				return []string{
					segs[i].Name,
					strconv.Itoa(segs[i].ID),
					strconv.Itoa(countConditions(segs[i].Rules)),
					segs[i].Description,
				}
			}, "segment", "segments")
	},
}

var segmentGetCmd = &cobra.Command{
	Use:     "get <segment>",
	Short:   "Show a segment and its rule tree",
	Example: "  flagsmith segment get beta-users",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveSegmentID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		seg, err := cred.client().GetSegment(cmd.Context(), projectID, id)
		if err != nil {
			return err
		}
		if !jsonOutput() {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"To get segment overrides, run flagsmith flag list --segment %d\n\n", seg.ID)
		}
		return renderSegment(cmd, seg)
	},
}

var segmentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a segment",
	Example: `  # rules from a file, - for stdin, or an inline JSON string
  flagsmith segment create beta-users --rules @rules.json --description "Beta cohort"

  # scope it to a feature (feature-specific segment)
  flagsmith segment create rollout --rules @rules.json --feature onboarding`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("rules") {
			return usageErrorf("a segment needs a rule tree — pass --rules")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		rules, err := rulesForWrite(segmentRulesFlag, cmd)
		if err != nil {
			return err
		}
		in := api.Segment{Name: args[0], Description: segmentDescriptionFlag, Rules: rules}
		if cmd.Flags().Changed("feature") {
			fid, err := resolveFeatureID(cmd, cred, projectID, segmentFeatureFlag)
			if err != nil {
				return err
			}
			in.Feature = &fid
		}
		seg, err := cred.client().CreateSegment(cmd.Context(), projectID, in)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created segment %s", label(seg.Name, seg.ID))
		return renderSegment(cmd, seg)
	},
}

var segmentUpdateCmd = &cobra.Command{
	Use:   "update <segment>",
	Short: "Update a segment's rule tree or fields",
	Example: `  # --rules replaces the whole tree
  flagsmith segment update beta-users --rules @rules.json
  flagsmith segment update beta-users --description "Updated cohort"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("rules") && !cmd.Flags().Changed("description") && !cmd.Flags().Changed("feature") {
			return usageErrorf("nothing to update")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveSegmentID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		seg, err := cred.client().GetSegment(cmd.Context(), projectID, id)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("rules") {
			if seg.Rules, err = rulesForWrite(segmentRulesFlag, cmd); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("description") {
			seg.Description = segmentDescriptionFlag
		}
		if cmd.Flags().Changed("feature") {
			fid, err := resolveFeatureID(cmd, cred, projectID, segmentFeatureFlag)
			if err != nil {
				return err
			}
			seg.Feature = &fid
		}
		updated, err := cred.client().UpdateSegment(cmd.Context(), projectID, id, *seg)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated segment %s", label(updated.Name, updated.ID))
		return renderSegment(cmd, updated)
	},
}

var segmentDeleteCmd = &cobra.Command{
	Use:     "delete <segment>",
	Short:   "Delete a segment",
	Example: "  flagsmith segment delete beta-users --yes",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveSegmentID(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		// The ref's name half only — never the typed id — with the cached
		// display name (seeded by name resolution) filling in when known.
		name := cache.Load(apiURL).Segments[strconv.Itoa(id)]
		if name == "" {
			name = nameRef(args[0])
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmed(cmd, fmt.Sprintf("delete segment %s", label(name, id)), "deleted"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteSegment(cmd.Context(), projectID, id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted segment %s", label(name, id))
		return nil
	},
}

// resolveSegmentID turns a segment reference (id or name) into an id.
func resolveSegmentID(cmd *cobra.Command, cred *activeCredential, projectID int, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		return id, nil
	}
	segs, err := cred.client().Segments(cmd.Context(), projectID, true)
	if err != nil {
		return 0, err
	}
	byID := idNameMap(segs, func(s api.Segment) (string, string) { return strconv.Itoa(s.ID), s.Name })
	_ = cache.Merge(apiURL, &cache.Names{Segments: byID}) // opportunistic
	return resolveIDRef(cmd, "segment", ref, byID,
		fmt.Errorf("segment %q not found in project %d", ref, projectID),
		hintSegmentList)
}

// resolveFeatureID turns a feature reference (id or name) into an id, without
// needing an environment.
func resolveFeatureID(cmd *cobra.Command, cred *activeCredential, projectID int, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		return id, nil
	}
	features, err := cred.client().Features(cmd.Context(), projectID, 0, 0, ref)
	if err != nil {
		return 0, err
	}
	if f := findFeature(features, ref); f != nil {
		return f.ID, nil
	}
	return 0, withHint(
		fmt.Errorf("feature %q not found in project %d", ref, projectID),
		hintFeatureList)
}

// rulesForWrite parses a --rules argument (a single SegmentRule), validates
// depth, encodes IN values for the wire, and wraps it in the API's rules array.
func rulesForWrite(arg string, cmd *cobra.Command) ([]api.SegmentRule, error) {
	raw, err := readRuleArg(cmd, arg)
	if err != nil {
		return nil, err
	}
	var rule api.SegmentRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return nil, fmt.Errorf("parsing --rules: %w", err)
	}
	if err := checkRuleDepth(&rule); err != nil {
		return nil, err
	}
	encodeInValues(&rule)
	return []api.SegmentRule{rule}, nil
}

// readRuleArg reads a --rules value: @file, - for stdin, or an inline string.
func readRuleArg(cmd *cobra.Command, arg string) ([]byte, error) {
	switch {
	case arg == "-":
		return io.ReadAll(cmd.InOrStdin())
	case strings.HasPrefix(arg, "@"):
		return os.ReadFile(arg[1:])
	default:
		return []byte(arg), nil
	}
}

// checkRuleDepth enforces the Admin API's two-level nesting cap.
func checkRuleDepth(rule *api.SegmentRule) error {
	for i := range rule.Rules {
		if len(rule.Rules[i].Rules) > 0 {
			return usageErrorf("segment rules nest at most two levels (a top-level rule of sub-rules, each holding conditions)")
		}
	}
	return nil
}

// encodeInValues rewrites IN condition values from an array to the JSON-array
// string the Admin API stores.
func encodeInValues(rule *api.SegmentRule) {
	for i := range rule.Conditions {
		c := &rule.Conditions[i]
		if c.Operator == "IN" {
			switch c.Value.(type) {
			case []any, []string:
				if b, err := json.Marshal(c.Value); err == nil {
					c.Value = string(b)
				}
			}
		}
	}
	for i := range rule.Rules {
		encodeInValues(&rule.Rules[i])
	}
}

// decodeInValues rewrites a stored IN value string back into an array for
// output: a [-prefixed value as JSON, otherwise comma-split.
func decodeInValues(rule *api.SegmentRule) {
	for i := range rule.Conditions {
		c := &rule.Conditions[i]
		if c.Operator == "IN" {
			if s, ok := c.Value.(string); ok {
				if strings.HasPrefix(s, "[") {
					var arr []any
					if json.Unmarshal([]byte(s), &arr) == nil {
						c.Value = arr
						continue
					}
				}
				c.Value = strings.Split(s, ",")
			}
		}
	}
	for i := range rule.Rules {
		decodeInValues(&rule.Rules[i])
	}
}

// topRule returns a segment's single top-level rule, wrapping several (only
// creatable outside the dashboard) under a synthetic ALL.
func topRule(rules []api.SegmentRule) *api.SegmentRule {
	switch len(rules) {
	case 0:
		return nil
	case 1:
		return &rules[0]
	default:
		return &api.SegmentRule{Type: "ALL", Rules: rules}
	}
}

func countConditions(rules []api.SegmentRule) int {
	n := 0
	for i := range rules {
		n += len(rules[i].Conditions)
		n += countConditions(rules[i].Rules)
	}
	return n
}

// toSegmentView decodes IN values and builds the curated view (stamping the
// rule $schema).
func toSegmentView(seg *api.Segment) segmentView {
	v := segmentView{ID: seg.ID, Name: seg.Name, Description: seg.Description, Feature: seg.Feature}
	if top := topRule(seg.Rules); top != nil {
		decodeInValues(top)
		rv := toRuleView(top, true)
		v.Rules = &rv
	}
	return v
}

func toRuleView(r *api.SegmentRule, top bool) ruleView {
	rv := ruleView{Type: r.Type}
	if top {
		rv.Schema = segmentRuleSchema
	}
	for i := range r.Conditions {
		c := r.Conditions[i]
		rv.Conditions = append(rv.Conditions, conditionView{Property: c.Property, Operator: c.Operator, Value: c.Value})
	}
	for i := range r.Rules {
		rv.Rules = append(rv.Rules, toRuleView(&r.Rules[i], false))
	}
	return rv
}

// renderSegment prints a segment's detail view + rule tree (human) or curated
// JSON.
func renderSegment(cmd *cobra.Command, seg *api.Segment) error {
	view := toSegmentView(seg)
	// toSegmentView decodes IN values in place, so the tree this walks is the
	// decoded one the view already describes.
	top := topRule(seg.Rules)
	return output.Render(cmd.OutOrStdout(), view, outputOpts(), func(w io.Writer) error {
		if err := output.Detail(w, []output.Field{
			{Label: "Segment", Value: label(seg.Name, seg.ID)},
			{Label: "Description", Value: seg.Description},
		}); err != nil {
			return err
		}
		if top != nil {
			fmt.Fprintln(w)
			renderRule(w, top, 0)
		}
		return nil
	})
}

func ruleTypeLabel(t string) string {
	switch t {
	case "ALL":
		return "All of the below:"
	case "ANY":
		return "Any of the below:"
	case "NONE":
		return "None of the below:"
	default:
		return t
	}
}

// renderRule prints one rule and its subtree as an indented, aligned view.
func renderRule(w io.Writer, r *api.SegmentRule, indent int) {
	pad := strings.Repeat("  ", indent)
	fmt.Fprintf(w, "%s%s\n", pad, ruleTypeLabel(r.Type))
	if len(r.Conditions) > 0 {
		var buf bytes.Buffer
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		for _, c := range r.Conditions {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Property, c.Operator, conditionValueDisplay(c.Value))
		}
		tw.Flush()
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			fmt.Fprintf(w, "%s  %s\n", pad, line)
		}
	}
	for i := range r.Rules {
		renderRule(w, &r.Rules[i], indent+1)
	}
}

func conditionValueDisplay(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(v)
	}
}

func init() {
	segmentListCmd.Flags().BoolVar(&segmentIncludeFeatureSpecific, "include-feature-specific", false,
		"include feature-specific segments")
	for _, c := range []*cobra.Command{segmentCreateCmd, segmentUpdateCmd} {
		c.Flags().StringVar(&segmentRulesFlag, "rules", "", "rule tree: @file, - for stdin, or an inline JSON string")
		c.Flags().StringVar(&segmentDescriptionFlag, "description", "", "segment description")
		c.Flags().StringVar(&segmentFeatureFlag, "feature", "", "scope to a feature (id or name), making it feature-specific")
	}
	segmentCmd.AddCommand(segmentListCmd, segmentGetCmd, segmentCreateCmd, segmentUpdateCmd, segmentDeleteCmd)
	rootCmd.AddCommand(segmentCmd)
}

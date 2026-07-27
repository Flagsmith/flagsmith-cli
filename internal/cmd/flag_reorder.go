package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var flagReorderCmd = &cobra.Command{
	Use:   "reorder <feature> <segment>...",
	Short: "Re-permute a feature's segment override priorities",
	Example: `  # priorities are assigned in the input order: beta-optin first
  flagsmith flag reorder checkout beta-optin us-adults`,
	Args: cobra.MinimumNArgs(2),
	RunE: runFlagReorder,
}

// runFlagReorder assigns priorities 0..n-1 to a feature's segment overrides in
// the input order, in one update-flag-v2 request (one published version under
// v2 versioning). The input must name every overridden segment exactly once —
// a partial list would make the result depend on the current order.
func runFlagReorder(cmd *cobra.Command, args []string) error {
	name := args[0]
	_, cred, projectID, env, err := flagContext(cmd)
	if err != nil {
		return err
	}
	features, err := cred.client().Features(cmd.Context(), projectID, env.ID, 0, searchRef(name))
	if err != nil {
		return err
	}
	feature := findFeatureByRef(features, name)
	if feature == nil {
		return withHint(
			fmt.Errorf("feature %q not found in %s", name, environmentLabel(env)),
			hintFlagList)
	}
	name = feature.Name // canonical from here on: the wire ref and messages

	fss, err := cred.client().FeatureSegments(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return err
	}
	current := make(map[int]api.FeatureSegment, len(fss))
	for _, fs := range fss {
		current[fs.Segment] = fs
	}

	// Resolve the input order and require an exact permutation of the
	// overridden segments before anything is written. A valid argument names
	// an overridden segment, so names resolve against the override rows
	// already in hand; only an unknown name falls back to the project-level
	// resolution, for its richer error.
	byID := make(map[string]string, len(fss))
	for _, fs := range fss {
		byID[strconv.Itoa(fs.Segment)] = fs.SegmentName
	}
	resolveRef := func(ref string) (int, error) {
		if id, err := strconv.Atoi(ref); err == nil {
			return id, nil
		}
		hits := matchByName(byID, ref)
		if len(hits) == 0 {
			return resolveSegmentID(cmd, cred, projectID, ref)
		}
		chosen, err := pickCandidate(cmd, "segment", "id", ref, hits, byID)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(chosen)
	}
	ordered := make([]int, 0, len(args)-1)
	seen := make(map[int]bool, len(args)-1)
	for _, ref := range args[1:] {
		id, err := resolveRef(ref)
		if err != nil {
			return err
		}
		if seen[id] {
			return usageErrorf("segment %s is listed twice", ref)
		}
		seen[id] = true
		if _, ok := current[id]; !ok {
			return usageErrorf("segment %s has no override for %s", ref, name)
		}
		ordered = append(ordered, id)
	}
	if len(ordered) < len(fss) {
		missing := make([]string, 0, len(fss)-len(ordered))
		for _, fs := range fss { // priority order keeps the message stable
			if !seen[fs.Segment] {
				missing = append(missing, segmentRef{ID: fs.Segment, Name: fs.SegmentName}.display())
			}
		}
		return usageErrorf("a reorder must list every overridden segment for %s (missing: %s)",
			name, strings.Join(missing, ", "))
	}

	// Each override echoes its current state and value; only priorities move.
	states, err := cred.client().FeatureStates(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return err
	}
	stateByFS := make(map[int]api.EnvironmentFeatureState, len(states))
	for _, s := range states {
		if s.FeatureSegment != nil {
			stateByFS[*s.FeatureSegment] = s
		}
	}
	req := api.UpdateFlagRequest{
		Feature: api.FeatureRef{Name: feature.Name},
		EnvironmentDefault: api.EnvironmentDefault{
			Enabled: flagEnabled(feature.EnvironmentState),
			Value:   featureValueFromScalar(currentScalar(feature.EnvironmentState)),
		},
	}
	for i, segmentID := range ordered {
		priority := i
		state := stateByFS[current[segmentID].ID]
		req.SegmentOverrides = append(req.SegmentOverrides, api.SegmentOverride{
			SegmentID: segmentID,
			Enabled:   state.Enabled,
			Value:     featureValueFromScalar(state.Value.Scalar()),
			Priority:  &priority,
		})
	}

	errOut := cmd.ErrOrStderr()
	prompt := fmt.Sprintf("Reorder %d segment overrides for %s in environment %s?", len(ordered), name, environmentLabel(env))
	if ok, err := confirmOrYes(cmd, prompt); err != nil {
		return err
	} else if !ok {
		fmt.Fprintln(errOut, "Aborted; nothing changed.")
		return nil
	}
	if err := cred.client().UpdateFlag(cmd.Context(), env.APIKey, req); err != nil {
		return err
	}
	output.Success(errOut, "Reordered %d segment overrides for %s in environment %s", len(ordered), name, environmentLabel(env))

	// Result model: print the resulting override list. It is fully known —
	// the write assigned priorities 0..n-1 in input order, and each
	// override's state was read (and echoed unchanged) before the write.
	views := make([]segmentFlagView, len(ordered))
	for i, segmentID := range ordered {
		fs := current[segmentID]
		state := stateByFS[fs.ID]
		views[i] = segmentFlagView{
			Feature:  feature.Name,
			Type:     featureTypeLabel(feature.Type),
			Segment:  segmentRef{ID: segmentID, Name: fs.SegmentName},
			Priority: i,
			Enabled:  state.Enabled,
			Value:    state.Value.Scalar(),
		}
	}
	return renderSegmentOverrideList(cmd, views)
}

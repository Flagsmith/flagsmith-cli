package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
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
// the input order, in one update-flag request (one published version under v2
// versioning). The input must name every overridden segment exactly once — a
// partial list would make the result depend on the current order.
func runFlagReorder(cmd *cobra.Command, args []string) error {
	name := args[0]
	_, cred, projectID, env, err := flagContext(cmd)
	if err != nil {
		return err
	}
	feature, err := requireFeature(cmd, cred, projectID, env, 0, name)
	if err != nil {
		return err
	}
	name = feature.Name // canonical for the wire ref and messages

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

	// Only priorities move: a partial write leaves every other property of
	// each override — its state, value and weights — exactly as it is.
	var req api.UpdateFlagRequest
	for i, segmentID := range ordered {
		priority := i
		req.SegmentOverrides = append(req.SegmentOverrides, api.SegmentOverrideUpdate{
			Segment:  api.SegmentTarget{ID: segmentID},
			Priority: &priority,
		})
	}

	errOut := cmd.ErrOrStderr()
	prompt := fmt.Sprintf("Reorder %d segment overrides for %s in environment %s?", len(ordered), name, environmentLabel(env))
	if ok, err := confirmed(cmd, prompt, "changed"); !ok || err != nil {
		return err
	}
	resp, err := cred.client().UpdateFlag(cmd.Context(), env.APIKey, feature.ID, req)
	if err != nil {
		return err
	}
	output.Success(errOut, "Reordered %d segment overrides for %s in environment %s", len(ordered), name, environmentLabel(env))

	// Result model: print the resulting override list, in the new priority
	// order the response reports. Only the segment names come from the rows
	// read before the write — the response identifies segments by id alone.
	views := make([]segmentFlagView, 0, len(resp.SegmentOverrides))
	for _, override := range resp.SegmentOverrides {
		scalar, err := scalarOfValue(override.Value)
		if err != nil {
			return err
		}
		views = append(views, segmentFlagView{
			Feature:  feature.Name,
			Type:     featureTypeLabel(feature.Type),
			Segment:  segmentRef{ID: override.Segment.ID, Name: current[override.Segment.ID].SegmentName},
			Priority: override.Priority,
			Enabled:  override.Enabled,
			Value:    scalar,
			Variants: variantViews(feature, weightsOf(override.Variants)),
		})
	}
	sort.SliceStable(views, func(a, b int) bool { return views[a].Priority < views[b].Priority })
	return renderSegmentOverrideList(cmd, views)
}

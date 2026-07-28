package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/bug"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
	"github.com/Flagsmith/flagsmith-cli/internal/prompt"
)

// Every exported Err* sentinel must have an explicit hint decision: a hintFor
// mapping, or an entry in consciouslyUnhinted recording that it stays plain.
// The source scan keeps the table honest: declaring a new sentinel anywhere
// under internal/ fails this test until the decision is recorded.
func TestEverySentinelHasAHintDecision(t *testing.T) {
	sentinels := map[string]error{
		"api.ErrPlanGated":            api.ErrPlanGated,
		"api.ErrQuotaExceeded":        api.ErrQuotaExceeded,
		"api.ErrWorkflowGated":        api.ErrWorkflowGated,
		"auth.ErrNotLoggedIn":         auth.ErrNotLoggedIn,
		"auth.ErrKeychainUnavailable": auth.ErrKeychainUnavailable,
		"auth.ErrRefreshFailed":       auth.ErrRefreshFailed,
		"auth.ErrServerSideKey":       auth.ErrServerSideKey,
		"auth.ErrLegacyAuthtoken":     auth.ErrLegacyAuthtoken,
		"auth.ErrNotMasterKey":        auth.ErrNotMasterKey,
		"bug.ErrUnexpected":           bug.ErrUnexpected,
		"config.ErrServerSideKey":     config.ErrServerSideKey,
		"prompt.ErrCancelled":         prompt.ErrCancelled,
	}

	// Sentinels reviewed and deliberately left without a hint.
	consciouslyUnhinted := map[string]bool{
		// The user chose to abort; there is nothing to recover from.
		"prompt.ErrCancelled": true,
	}

	found := scanSentinelNames(t)
	if len(found) == 0 {
		t.Fatal("source scan found no Err* sentinels — the scan is broken")
	}
	for _, name := range found {
		if _, ok := sentinels[name]; !ok {
			t.Errorf("%s is not in the sentinel table — add it and decide its hint", name)
		}
	}
	byName := make(map[string]bool, len(found))
	for _, name := range found {
		byName[name] = true
	}
	for name, err := range sentinels {
		if !byName[name] {
			t.Errorf("%s is in the sentinel table but not in the source — remove the stale entry", name)
			continue
		}
		if consciouslyUnhinted[name] {
			if hintFor(err) != "" {
				t.Errorf("%s is marked consciously unhinted but hintFor returns %q", name, hintFor(err))
			}
			continue
		}
		if hintFor(err) == "" {
			t.Errorf("%s has no hint — map it in hintFor, or record it in consciouslyUnhinted", name)
		}
	}
}

// scanSentinelNames returns "pkg.ErrName" for every exported Err* package var
// declared under internal/, tests excluded.
func scanSentinelNames(t *testing.T) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					if strings.HasPrefix(n.Name, "Err") && ast.IsExported(n.Name) {
						names = append(names, f.Name.Name+"."+n.Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/ for sentinels: %v", err)
	}
	return names
}

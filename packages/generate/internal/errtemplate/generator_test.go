package errtemplate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGeneratesAndPreservesLocaleText(t *testing.T) {
	moduleRoot := createTestModule(t, `package sample

import modularerrs "github.com/wplbyx/modular/packages/errs"

const userID = "user_id"

var UserNotFound = modularerrs.Define(
	"USER_NOT_FOUND",
	modularerrs.Template("user %v not found", modularerrs.Name(userID)),
)
`)
	output := filepath.Join(moduleRoot, "locales")
	config := Config{
		Root:      moduleRoot,
		Packages:  []string{"./..."},
		Output:    output,
		Languages: []string{"zh-cn", "en-US"},
	}

	require.NoError(t, Run(context.Background(), config))
	zhFilename := filepath.Join(output, "zh-CN.yaml")
	zh, err := os.ReadFile(zhFilename)
	require.NoError(t, err)
	assert.Contains(t, string(zh), "# slots: user_id")
	assert.Contains(t, string(zh), `USER_NOT_FOUND: "user {{.user_id}} not found"`)

	translated := strings.Replace(string(zh), `"user {{.user_id}} not found"`, `"用户 {{.user_id}} 不存在"`, 1)
	translated = strings.Replace(translated, "# slots: user_id", "# slots: user_id\n# product note", 1)
	require.NoError(t, os.WriteFile(zhFilename, []byte(translated), 0o644))
	require.NoError(t, Run(context.Background(), config))

	preserved, err := os.ReadFile(zhFilename)
	require.NoError(t, err)
	assert.Contains(t, string(preserved), `"用户 {{.user_id}} 不存在"`)
	assert.Contains(t, string(preserved), "# product note")

	config.Check = true
	require.NoError(t, Run(context.Background(), config))
}

func TestRunRejectsSlotChangesAndStaleReasonsWithoutWriting(t *testing.T) {
	moduleRoot := createTestModule(t, `package sample

import "github.com/wplbyx/modular/packages/errs"

var UserNotFound = errs.Define(
	"USER_NOT_FOUND",
	errs.Template("user %v not found", errs.Name("user_id")),
)
`)
	output := filepath.Join(moduleRoot, "locales")
	require.NoError(t, os.MkdirAll(output, 0o755))
	filename := filepath.Join(output, "en-US.yaml")
	original := "# existing\nUNKNOWN: \"Request failed\"\nUSER_NOT_FOUND: \"User {{.other_id}} missing\"\nSTALE: \"stale\"\n"
	require.NoError(t, os.WriteFile(filename, []byte(original), 0o644))

	err := Run(context.Background(), Config{
		Root:      moduleRoot,
		Output:    output,
		Languages: []string{"en-US"},
	})
	require.Error(t, err)
	current, readErr := os.ReadFile(filename)
	require.NoError(t, readErr)
	assert.Equal(t, original, string(current))
}

func TestRunCheckReportsMissingReasonsWithoutWriting(t *testing.T) {
	moduleRoot := createTestModule(t, `package sample

import "github.com/wplbyx/modular/packages/errs"

var Busy = errs.Define("BUSY", errs.Template("busy"))
`)
	output := filepath.Join(moduleRoot, "locales")
	require.NoError(t, os.MkdirAll(output, 0o755))
	filename := filepath.Join(output, "en-US.yaml")
	original := "# slots: none\nUNKNOWN: \"Request failed\"\n"
	require.NoError(t, os.WriteFile(filename, []byte(original), 0o644))

	err := Run(context.Background(), Config{
		Root:      moduleRoot,
		Output:    output,
		Languages: []string{"en-US"},
		Check:     true,
	})
	require.ErrorContains(t, err, "missing reasons: BUSY")
	current, readErr := os.ReadFile(filename)
	require.NoError(t, readErr)
	assert.Equal(t, original, string(current))
}

func TestRunAppendsReasonToExistingLocale(t *testing.T) {
	moduleRoot := createTestModule(t, `package sample

import "github.com/wplbyx/modular/packages/errs"

var Busy = errs.Define("BUSY", errs.Template("busy"))
`)
	output := filepath.Join(moduleRoot, "locales")
	require.NoError(t, os.MkdirAll(output, 0o755))
	filename := filepath.Join(output, "en-US.yaml")
	original := "# slots: none\nUNKNOWN: \"Request failed\"\n"
	require.NoError(t, os.WriteFile(filename, []byte(original), 0o640))

	require.NoError(t, Run(context.Background(), Config{
		Root:      moduleRoot,
		Output:    output,
		Languages: []string{"en-US"},
	}))
	updated, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Contains(t, string(updated), `BUSY: "busy"`)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filename)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	}
}

func TestRunRejectsConflictingDefinitions(t *testing.T) {
	moduleRoot := createTestModule(t, `package sample

import "github.com/wplbyx/modular/packages/errs"

var First = errs.Define("BUSY", errs.Template("busy"))
var Second = errs.Define("BUSY", errs.Template("still busy"))
`)
	err := Run(context.Background(), Config{
		Root:      moduleRoot,
		Output:    filepath.Join(moduleRoot, "locales"),
		Languages: []string{"en-US"},
	})
	require.ErrorContains(t, err, "conflicting definition for BUSY")
}

func createTestModule(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	repositoryRoot := repositoryRoot(t)
	goMod := "module example.com/errorfixture\n\ngo 1.26.0\n\nrequire github.com/wplbyx/modular v0.0.0\n\nreplace github.com/wplbyx/modular => " + filepath.ToSlash(repositoryRoot) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "errors.go"), []byte(source), 0o644))
	return root
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
	require.NoError(t, err)
	return root
}

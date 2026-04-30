package initcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetFlags clears package-level flag vars so test ordering does not leak state.
func resetFlags() {
	outputPath = defaultOutputPath
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags()
	cmd := NewInitCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// Drift guard: the embedded template and the OSK credentials parser must stay in
// sync. A failure here means the template no longer parses cleanly under
// types.NewOSKCredentialsFromFile and the asset needs updating.
func TestEmbeddedTemplate_RoundTripParse(t *testing.T) {
	require.NotEmpty(t, oskTemplate, "embedded template bytes must be non-empty")

	path := filepath.Join(t.TempDir(), "osk-credentials.yaml")
	require.NoError(t, os.WriteFile(path, oskTemplate, 0600))

	creds, errs := types.NewOSKCredentialsFromFile(path)
	require.Empty(t, errs, "embedded template must parse and validate cleanly")
	require.NotNil(t, creds)

	require.Len(t, creds.Clusters, 1, "template must declare exactly one example cluster")

	cluster := creds.Clusters[0]
	methods := cluster.GetAuthMethods()
	require.Len(t, methods, 1, "template must enable exactly one auth method")
	assert.Equal(t, types.AuthTypeSASLSCRAM, methods[0], "default active method must be SASL/SCRAM")

	require.NotNil(t, cluster.AuthMethod.SASLScram)
	assert.True(t, cluster.AuthMethod.SASLScram.Use)
	assert.Equal(t, "SHA256", cluster.AuthMethod.SASLScram.Mechanism, "default SCRAM mechanism must be SHA256")
	// Regression guard: substituting a real-looking value here would silently ship a
	// template that scans without warning the user to fill in their password.
	assert.Equal(t, "REPLACE_ME", cluster.AuthMethod.SASLScram.Password)
}

func TestInit_WritesFileAndPrintsNextStep(t *testing.T) {
	target := filepath.Join(t.TempDir(), "osk-credentials.yaml")

	out, err := runCmd(t, "--output", target)
	require.NoError(t, err)

	written, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, oskTemplate, written, "written file must equal embedded template bytes")

	creds, errs := types.NewOSKCredentialsFromFile(target)
	require.Empty(t, errs, "written file must be parser-valid")
	require.NotNil(t, creds)

	expectedNextStep := "kcp scan clusters --source-type osk --credentials-file " + target
	assert.Contains(t, out, expectedNextStep, "stdout must contain shell-ready next-step invocation with the actual output path")
}

// Mutates os.Chdir, so this test cannot run in parallel and other cwd-sensitive
// tests in this file must also stay sequential.
func TestInit_DefaultPathInCwd(t *testing.T) {
	dir := t.TempDir()
	originalCwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalCwd) })

	require.NoError(t, os.Chdir(dir))

	out, err := runCmd(t)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(dir, defaultOutputPath))
	require.NoError(t, statErr, "default invocation must write %s in the working directory", defaultOutputPath)

	expectedNextStep := "kcp scan clusters --source-type osk --credentials-file " + defaultOutputPath
	assert.Contains(t, out, expectedNextStep, "printed next-step must reference the relative default path")
}

func TestInit_FailsOnExistingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "osk-credentials.yaml")
	original := []byte("# existing user content; do not clobber\n")
	require.NoError(t, os.WriteFile(target, original, 0600))

	_, err := runCmd(t, "--output", target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), target, "error must name the existing file")

	after, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, original, after, "existing file must remain byte-for-byte unchanged")
}

func TestInit_HelpTextSurfacesOSKAndExcludesMSK(t *testing.T) {
	cmd := NewInitCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())

	helpOutput := buf.String()
	lower := strings.ToLower(helpOutput)
	assert.True(t,
		strings.Contains(lower, "osk") || strings.Contains(lower, "open source kafka"),
		"init --help must mention OSK or Open Source Kafka",
	)
	assert.Contains(t, lower, "kcp discover", "init --help must point MSK users at kcp discover")
}

func TestInit_FailsWhenParentDirMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "credentials.yaml")

	_, err := runCmd(t, "--output", missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing, "error must surface the failing path")
}

// O_EXCL on the open path must treat a symlink-at-target the same as a file-at-target,
// otherwise we'd silently overwrite whatever the link points at.
func TestInit_SymlinkAtTargetDoesNotFollow(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.yaml")
	original := []byte("# protected by symlink test\n")
	require.NoError(t, os.WriteFile(realFile, original, 0600))

	link := filepath.Join(dir, "credentials.yaml")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	_, err := runCmd(t, "--output", link)
	require.Error(t, err, "init must refuse to write through a pre-existing symlink")

	after, readErr := os.ReadFile(realFile)
	require.NoError(t, readErr)
	assert.Equal(t, original, after, "symlink target must not have been overwritten")
}

func TestInit_RegisteredOnRoot(t *testing.T) {
	// Use a fresh root rather than the real RootCmd to avoid its logging side effects
	// and version banner during test execution.
	root := &cobra.Command{Use: "kcp"}
	root.AddCommand(NewInitCmd())

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	help := buf.String()
	assert.Contains(t, help, "init", "root help must list 'init' as a subcommand")
	lower := strings.ToLower(help)
	assert.True(t,
		strings.Contains(lower, "osk") || strings.Contains(lower, "open source kafka"),
		"root help short text for init must mention OSK / Open Source Kafka",
	)
}

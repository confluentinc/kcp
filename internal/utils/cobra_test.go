package utils

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newNounTree(verbHidden bool) (root *cobra.Command, noun *cobra.Command, verbRan *bool) {
	ran := false
	verb := &cobra.Command{
		Use:    "generate",
		Short:  "Generate topics",
		Hidden: verbHidden,
		RunE: func(*cobra.Command, []string) error {
			ran = true
			return nil
		},
	}
	noun = NewNounCmd("topics", verb)
	root = &cobra.Command{Use: "kcp", SilenceErrors: true}
	root.AddCommand(noun)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root, noun, &ran
}

func TestNewNounCmd_RunsVerb(t *testing.T) {
	root, _, ran := newNounTree(false)
	root.SetArgs([]string{"topics", "generate"})

	require.NoError(t, root.Execute())
	assert.True(t, *ran)
}

func TestNewNounCmd_MissingVerbErrors(t *testing.T) {
	root, _, ran := newNounTree(false)
	root.SetArgs([]string{"topics"})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"kcp topics generate"`)
	assert.False(t, *ran)
}

func TestNewNounCmd_UnknownVerbErrors(t *testing.T) {
	root, _, ran := newNounTree(false)
	root.SetArgs([]string{"topics", "genrate"})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "genrate" for "kcp topics"`)
	assert.False(t, *ran)
}

func TestNewNounCmd_CopiesShort(t *testing.T) {
	_, noun, _ := newNounTree(false)
	assert.Equal(t, "Generate topics", noun.Short)
}

func TestNewNounCmd_HiddenVerbHidesNoun(t *testing.T) {
	_, visible, _ := newNounTree(false)
	assert.True(t, visible.IsAvailableCommand())

	_, hidden, _ := newNounTree(true)
	assert.False(t, hidden.IsAvailableCommand())
}

func TestNewNounCmd_UnknownFlagWithoutVerbHintsAtVerb(t *testing.T) {
	root, _, ran := newNounTree(false)
	root.SetArgs([]string{"topics", "--state-file", "x.json", "--mode", "new"})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"kcp topics generate"`)
	assert.False(t, *ran)
}

func TestNewNounCmd_FlagsStillReachVerb(t *testing.T) {
	root, noun, ran := newNounTree(false)
	verb, _, err := noun.Find([]string{"generate"})
	require.NoError(t, err)
	var stateFile string
	verb.Flags().StringVar(&stateFile, "state-file", "", "")
	root.SetArgs([]string{"topics", "generate", "--state-file", "x.json"})

	require.NoError(t, root.Execute())
	assert.True(t, *ran)
	assert.Equal(t, "x.json", stateFile)
}

func newGroupTree() (root *cobra.Command, leafRan *bool) {
	ran := false
	leaf := &cobra.Command{
		Use: "iam",
		RunE: func(*cobra.Command, []string) error {
			ran = true
			return nil
		},
	}
	group := NewGroupCmd(&cobra.Command{Use: "acls"})
	group.AddCommand(leaf)
	root = &cobra.Command{Use: "kcp", SilenceErrors: true}
	root.AddCommand(group)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root, &ran
}

func TestNewGroupCmd_RunsSubcommand(t *testing.T) {
	root, ran := newGroupTree()
	root.SetArgs([]string{"acls", "iam"})

	require.NoError(t, root.Execute())
	assert.True(t, *ran)
}

func TestNewGroupCmd_UnknownSubcommandErrors(t *testing.T) {
	root, ran := newGroupTree()
	root.SetArgs([]string{"acls", "iamm"})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "iamm" for "kcp acls"`)
	assert.False(t, *ran)
}

func TestNewGroupCmd_BareInvocationPrintsHelp(t *testing.T) {
	root, ran := newGroupTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"acls"})

	require.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "Available Commands")
	assert.False(t, *ran)
}

package utils

import (
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

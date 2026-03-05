package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommandHelp(t *testing.T) {
	cmd := newRootCmd()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "--config")
	assert.Contains(t, output, "--no-tui")
	assert.Contains(t, output, "--log-file")
	assert.Contains(t, output, "--log-level")
	assert.Contains(t, output, "--dry-run")
	assert.Contains(t, output, "WORKFLOW.md")
}

func TestConfigFlagRequired(t *testing.T) {
	cmd := newRootCmd()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config")
}

func TestFlagDefaults(t *testing.T) {
	cmd := newRootCmd()

	tests := []struct {
		name     string
		flag     string
		defValue string
	}{
		{"log-file default", "log-file", "symphony-charm.log"},
		{"log-level default", "log-level", "info"},
		{"no-tui default", "no-tui", "false"},
		{"dry-run default", "dry-run", "false"},
		{"config default", "config", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flag)
			require.NotNil(t, f, "flag %q should exist", tt.flag)
			assert.Equal(t, tt.defValue, f.DefValue)
		})
	}
}

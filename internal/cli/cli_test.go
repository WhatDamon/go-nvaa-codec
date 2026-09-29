package cli

import (
	"reflect"
	"testing"
)

// TestSplitOperandAllowsEitherOrder is the whole reason this helper exists: the
// operand may come before or after the flags that belong to the program.
func TestSplitOperandAllowsEitherOrder(t *testing.T) {
	values := map[string]bool{"w": true, "frames": true}

	cases := []struct {
		args    []string
		want    []string
		operand string
	}{
		{[]string{"clip.nvaa"}, nil, "clip.nvaa"},
		{[]string{"clip.nvaa", "-w", "80"}, []string{"-w", "80"}, "clip.nvaa"},
		{[]string{"-w", "80", "clip.nvaa"}, []string{"-w", "80"}, "clip.nvaa"},
		{[]string{"--frames=3", "clip.nvaa", "--ansi"}, []string{"--frames=3", "--ansi"}, "clip.nvaa"},
		{[]string{"--ansi", "clip.nvaa"}, []string{"--ansi"}, "clip.nvaa"},
	}

	for _, tc := range cases {
		rest, operand, err := SplitOperand(tc.args, values)
		if err != nil {
			t.Errorf("SplitOperand(%q) failed: %v", tc.args, err)
			continue
		}
		if operand != tc.operand {
			t.Errorf("SplitOperand(%q) found operand %q, want %q", tc.args, operand, tc.operand)
		}
		if !reflect.DeepEqual(rest, tc.want) {
			t.Errorf("SplitOperand(%q) left %q for the flags, want %q", tc.args, rest, tc.want)
		}
	}
}

// TestSplitOperandRefusesWhatItCannotRead covers the three ways the arguments
// can be wrong, so that a missing file is reported as one rather than as a flag
// error somewhere later.
func TestSplitOperandRefusesWhatItCannotRead(t *testing.T) {
	values := map[string]bool{"w": true}

	bad := [][]string{
		{},                   // nothing at all
		{"-w"},               // a flag whose value never arrives
		{"a.nvaa", "b.nvaa"}, // two operands
		{"-w", "80"},         // flags but no file
	}

	for _, args := range bad {
		if _, _, err := SplitOperand(args, values); err == nil {
			t.Errorf("SplitOperand(%q) returned no error", args)
		}
	}
}

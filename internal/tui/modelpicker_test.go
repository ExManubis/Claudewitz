package tui

import (
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
)

func TestModelPickerEffort(t *testing.T) {
	p := &modelPicker{
		models: []claude.ModelInfo{
			{Value: "opus", SupportsEffort: true, SupportedEffortLevels: []string{"low", "high", "max"}},
			{Value: "old"},
		},
		effort: map[string]string{},
	}
	// With no effort in force, "default" comes first.
	if lv := p.levels(); len(lv) != 4 || lv[0] != "" {
		t.Fatalf("levels = %q", lv)
	}
	p.shiftEffort(1)
	p.shiftEffort(1)
	if got := p.effort["opus"]; got != "high" {
		t.Fatalf("after two steps: %q", got)
	}
	p.shiftEffort(5)
	if got := p.effort["opus"]; got != "max" {
		t.Fatalf("should clamp at max, got %q", got)
	}
	// Once a level is in force, default can't be chosen again.
	p.curEff = "high"
	if lv := p.levels(); lv[0] != "low" {
		t.Fatalf("levels = %q", lv)
	}
	p.sel = 1
	if lv := p.levels(); lv != nil {
		t.Fatalf("model without effort support should have no levels, got %q", lv)
	}
}

package contracts

import (
	"encoding/json"
	"strings"
	"testing"

	"app/internal/domain/preferences"
)

func TestContractsActionsEventsUniqueAndValid(t *testing.T) {
	seenActions := make(map[string]struct{})
	for _, a := range Actions() {
		if !strings.Contains(a.Name, ":") {
			t.Fatalf("invalid action name %q", a.Name)
		}
		if _, dup := seenActions[a.Name]; dup {
			t.Fatalf("duplicate action %q", a.Name)
		}
		seenActions[a.Name] = struct{}{}
	}
	if len(ActionNames()) != len(Actions()) {
		t.Fatalf("ActionNames len mismatch")
	}

	seenEvents := make(map[string]struct{})
	for _, e := range Events() {
		if !strings.Contains(e.Name, ":") {
			t.Fatalf("invalid event name %q", e.Name)
		}
		if _, dup := seenEvents[e.Name]; dup {
			t.Fatalf("duplicate event %q", e.Name)
		}
		seenEvents[e.Name] = struct{}{}
	}
	if len(EventNames()) != len(Events()) {
		t.Fatalf("EventNames len mismatch")
	}
}

func TestPayloadValidationAndUnmarshalJSON(t *testing.T) {
	if err := (PreferencesPatch{}).Validate(); err == nil {
		t.Fatal("expected empty PreferencesPatch to fail validation")
	}
	theme := preferences.Theme("dark")
	if err := (PreferencesPatch{Theme: &theme}).Validate(); err != nil {
		t.Fatalf("valid PreferencesPatch rejected: %v", err)
	}

	if err := (MediaSignURLPayload{}).Validate(); err == nil {
		t.Fatal("expected empty MediaSignURLPayload to fail validation")
	}
	if err := (MediaSignURLPayload{AssetID: "a1", Purpose: "preview"}).Validate(); err != nil {
		t.Fatalf("valid MediaSignURLPayload rejected: %v", err)
	}

	if err := (StudioQueueEnqueuePayload{EpisodeID: "ep1", Stage: "unknown"}).Validate(); err == nil {
		t.Fatal("expected unsupported queue stage to fail validation")
	}
	if err := (StudioQueueEnqueuePayload{EpisodeID: "ep1", Stage: "lipsync"}).Validate(); err != nil {
		t.Fatalf("valid queue stage rejected: %v", err)
	}

	if err := (StudioPricingUpsertPayload{ProviderID: "p", ModelID: "m", Capability: "image", Unit: "unit", UnitPriceUSD: -1}).Validate(); err == nil {
		t.Fatal("expected negative pricing to fail validation")
	}

	// Custom UnmarshalJSON tolerates both snake_case and alias keys / stringified numbers cleanly.
	var grid GridSplitInput
	if err := json.Unmarshal([]byte(`{"image_path":"in.png","rows":2,"cols":3}`), &grid); err != nil {
		t.Fatalf("GridSplitInput UnmarshalJSON failed: %v", err)
	}
	if grid.ImagePath.LocalURL != "in.png" || grid.Rows != 2 || grid.Cols != 3 {
		t.Fatalf("unexpected GridSplitInput values: %+v", grid)
	}
}

package provider

import "testing"

func TestMetaDiscoverable(t *testing.T) {
	yes := []string{IDOpenAI, IDGeminiAIStudio, IDAnthropic, IDGrok, IDAgnes, IDDashScope, IDMiniMax}
	no := []string{IDKling, IDVidu, IDArk, IDArkAgentPlan, IDGeminiVertex}
	for _, id := range yes {
		if m, ok := Lookup(id); !ok || !m.Discoverable() {
			t.Errorf("%s should be discoverable", id)
		}
	}
	for _, id := range no {
		if m, ok := Lookup(id); !ok || m.Discoverable() {
			t.Errorf("%s should not be discoverable", id)
		}
	}
	if len(yes)+len(no) != len(Registry()) {
		t.Errorf("registry has %d providers, test covers %d", len(Registry()), len(yes)+len(no))
	}
}

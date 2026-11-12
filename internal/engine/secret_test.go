package engine

import "testing"

func TestValidateSpecSecretRule(t *testing.T) {
	base := func(p Param) Spec {
		return Spec{Type: "t.x", Label: "X", Description: "d", Params: []Param{p}}
	}
	if err := ValidateSpec(base(Param{Name: "k", Label: "K", Kind: ParamText, Secret: true})); err != nil {
		t.Fatalf("valid secret text rejected: %v", err)
	}
	bad := []Param{
		{Name: "k", Label: "K", Kind: ParamText, Secret: true, Default: "x"},
		{Name: "k", Label: "K", Kind: ParamToggle, Secret: true},
		{Name: "k", Label: "K", Kind: ParamInteger, Secret: true},
		{Name: "k", Label: "K", Kind: ParamSelect, Options: []string{"a"}, Secret: true},
	}
	for i, p := range bad {
		if err := ValidateSpec(base(p)); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

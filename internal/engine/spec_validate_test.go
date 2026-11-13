package engine

import (
	"strings"
	"testing"
)

func validSpec() Spec {
	return Spec{
		Type: "tool.x", Label: "X", Description: "Does X to images.",
		Inputs:  []Port{{Name: "image", Label: "Image", Type: PortImage}},
		Outputs: []Port{{Name: "image", Label: "Result", Type: PortImage}},
		Params: []Param{
			{Name: "inplace", Label: "Overwrite", Kind: ParamToggle, Default: "false", Destructive: true},
			{Name: "workers", Label: "Workers", Kind: ParamInteger, Min: 0, Max: 64, Default: "0"},
			{Name: "corner", Label: "Corner", Kind: ParamSelect, Options: []string{"a", "b"}, Default: "a"},
			{Name: "output_dir", Label: "Out", Kind: ParamText, Role: ParamRoleOutputDir},
		},
	}
}

func TestValidateSpecAcceptsACompleteSpec(t *testing.T) {
	if err := ValidateSpec(validSpec()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecRejectsWhatTheAgentCannotUse(t *testing.T) {
	cases := map[string]func(*Spec){
		"description":           func(s *Spec) { s.Description = "" },
		"select needs":          func(s *Spec) { s.Params[2].Options = nil },
		"not one of":            func(s *Spec) { s.Params[2].Default = "z" },
		"outside":               func(s *Spec) { s.Params[1].Default = "99" },
		"only toggles":          func(s *Spec) { s.Params[1].Destructive = true },
		"must default to false": func(s *Spec) { s.Params[0].Default = "true" },
		"output_dir role":       func(s *Spec) { s.Params[3].Kind = ParamInteger },
		"duplicates":            func(s *Spec) { s.Params[3].Name = "image" },
		"unknown type":          func(s *Spec) { s.Inputs[0].Type = "binary" },
	}
	for want, mutate := range cases {
		s := validSpec()
		s.Params = append([]Param{}, s.Params...)
		s.Inputs = append([]Port{}, s.Inputs...)
		mutate(&s)
		err := ValidateSpec(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

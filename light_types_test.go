package driversdk

import "testing"

func TestNormalizeLightType(t *testing.T) {
	for _, lt := range LightTypes {
		if got, ok := NormalizeLightType(lt); got != lt || !ok {
			t.Errorf("NormalizeLightType(%q) = %q, %v", lt, got, ok)
		}
	}
	for in, want := range map[string]LightType{
		"step_light": LightTypeStepLight,
		"Step-Light": LightTypeStepLight,
		" led ":      LightTypeLED,
		"CHANDELIER": LightTypeChandelier,
	} {
		if got, ok := NormalizeLightType(in); got != want || !ok {
			t.Errorf("NormalizeLightType(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "  ", "Spot", "Wall Sconce"} {
		if got, ok := NormalizeLightType(in); got != "" || ok {
			t.Errorf("NormalizeLightType(%q) = %q, %v; want none", in, got, ok)
		}
	}
}

package cards

import "testing"

func TestLookupByTheNameNvidiaSmiPrints(t *testing.T) {
	for name, want := range map[string]Profile{
		"Quadro RTX 4000": {NVENC: 1, Generation: "7th (Turing)"},
		"NVIDIA L4":       {NVENC: 2, Generation: "8th (Ada)"},
		"Tesla T4":        {NVENC: 1, Generation: "7th (Turing)"},
		"NVIDIA T4":       {NVENC: 1, Generation: "7th (Turing)"},
		"NVIDIA A10":      {NVENC: 1, Generation: "7th (Ampere)"},
	} {
		p, ok := Lookup(name)
		if !ok || p.NVENC != want.NVENC || p.Generation != want.Generation || p.SessionLimit != 0 || p.Source == "" {
			t.Errorf("%s: %+v %v", name, p, ok)
		}
	}
	if p, ok := Lookup("NVIDIA GeForce RTX 4090"); !ok || p.SessionLimit != GeForceSessionLimit || p.Source == "" {
		t.Errorf("GeForce: %+v %v", p, ok)
	}
	for _, name := range []string{"NVIDIA A100-SXM4-80GB", "NVIDIA A10G", "Quadro RTX 4000 Ada", ""} {
		if p, ok := Lookup(name); ok {
			t.Errorf("%q is not a card in the table: %+v", name, p)
		}
	}
}

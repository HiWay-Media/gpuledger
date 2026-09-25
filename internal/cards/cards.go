// Package cards knows what NVIDIA publishes per card that the driver does not report:
// how many NVENC engines it has and whether the driver caps concurrent encode sessions.
// Every number carries its source. Thermal limits are not here: the datasheets give
// only the ambient range, and the driver reports each card's own (see nvidia).
package cards

import "strings"

// Matrix is NVIDIA's Video Encode and Decode GPU Support Matrix, read 2026-09-25.
const Matrix = "https://developer.nvidia.com/video-encode-and-decode-gpu-support-matrix-new (read 2026-09-25)"

// GeForceSessionLimit is the concurrent NVENC session cap the matrix lists for
// GeForce cards; NVIDIA has raised it before (8 in 2024–25). Re-check before a release.
const GeForceSessionLimit = 12

// Profile is one card as the matrix lists it. SessionLimit 0 means "Unrestricted".
type Profile struct {
	Model        string `json:"model"`
	NVENC        int    `json:"nvenc"`
	Generation   string `json:"generation"`
	SessionLimit int    `json:"sessionLimit"`
	Source       string `json:"source"`
}

// byName is keyed by the name nvidia-smi prints, exactly: "A10" must not match an
// A100 or an A10G, nor "Quadro RTX 4000" its Ada successor.
var byName = map[string]Profile{
	"Quadro RTX 4000": {Model: "Quadro RTX 4000", NVENC: 1, Generation: "7th (Turing)", Source: Matrix},
	"NVIDIA L4":       {Model: "L4", NVENC: 2, Generation: "8th (Ada)", Source: Matrix},
	"Tesla T4":        {Model: "T4", NVENC: 1, Generation: "7th (Turing)", Source: Matrix},
	"NVIDIA T4":       {Model: "T4", NVENC: 1, Generation: "7th (Turing)", Source: Matrix},
	"NVIDIA A10":      {Model: "A10", NVENC: 1, Generation: "7th (Ampere)", Source: Matrix},
}

// Lookup returns the card's profile. GeForce cards are one class: the cap is the
// driver's, the same across the line.
func Lookup(name string) (Profile, bool) {
	name = strings.TrimSpace(name)
	if p, ok := byName[name]; ok {
		return p, true
	}
	if strings.Contains(name, "GeForce") {
		return Profile{Model: name, SessionLimit: GeForceSessionLimit, Source: Matrix}, true
	}
	return Profile{}, false
}

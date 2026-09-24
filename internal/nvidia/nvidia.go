// Package nvidia reads what the driver knows about each GPU and each process on it.
// It shells out to nvidia-smi's CSV queries rather than binding NVML, so the binary
// stays static and portable; the queries used are documented and stable.
package nvidia

import (
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GPU is one physical device as nvidia-smi reports it.
type GPU struct {
	Index           int     `json:"index"`
	UUID            string  `json:"uuid"`
	Model           string  `json:"model"`
	BusID           string  `json:"busId"`
	MemoryUsedMiB   int     `json:"memoryUsedMiB"`
	MemoryTotalMiB  int     `json:"memoryTotalMiB"`
	UtilizationPct  int     `json:"utilizationPct"`
	MemUtilPct      int     `json:"memoryUtilizationPct"`
	TemperatureC    int     `json:"temperatureC"`
	PowerW          float64 `json:"powerW"`
	EncoderSessions int     `json:"encoderSessions"`
	EncoderFPS      float64 `json:"encoderAverageFps"`
}

// Process is one PID the driver sees on a GPU.
type Process struct {
	GPUUUID       string `json:"gpuUuid"`
	PID           int    `json:"pid"`
	UsedMemoryMiB int    `json:"usedMemoryMiB"`
	// Binary name only, never the arguments: an argument can carry anything.
	Name string `json:"name"`
}

// GPUQuery and ProcessQuery are the exact field lists asked of nvidia-smi.
const (
	GPUQuery     = "index,uuid,name,pci.bus_id,memory.used,memory.total,utilization.gpu,utilization.memory,temperature.gpu,power.draw,encoder.stats.sessionCount,encoder.stats.averageFps"
	ProcessQuery = "gpu_uuid,pid,used_memory,process_name"
)

// Runner executes nvidia-smi; tests substitute fixtures.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct{ bin string }

func (r execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, r.bin, args...).Output()
}

// NewRunner runs the given nvidia-smi binary (default "nvidia-smi").
func NewRunner(bin string) Runner {
	if bin == "" {
		bin = "nvidia-smi"
	}
	return execRunner{bin: bin}
}

// Query returns the GPUs and their processes. An error means the driver could not be
// asked at all — a finding in itself, never a crash.
func Query(ctx context.Context, r Runner) ([]GPU, []Process, error) {
	out, err := r.Run(ctx, "--query-gpu="+GPUQuery, "--format=csv,noheader,nounits")
	if err != nil {
		return nil, nil, fmt.Errorf("nvidia-smi query-gpu: %w", err)
	}
	gpus, err := ParseGPUs(string(out))
	if err != nil {
		return nil, nil, err
	}
	pout, err := r.Run(ctx, "--query-compute-apps="+ProcessQuery, "--format=csv,noheader,nounits")
	if err != nil {
		return gpus, nil, fmt.Errorf("nvidia-smi query-compute-apps: %w", err)
	}
	procs, err := ParseProcesses(string(pout))
	return gpus, procs, err
}

func rows(text string) ([][]string, error) {
	rd := csv.NewReader(strings.NewReader(strings.TrimSpace(text)))
	rd.TrimLeadingSpace = true
	rd.FieldsPerRecord = -1
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return rd.ReadAll()
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "[N/A]" || s == "N/A" || s == "[Not Supported]" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f)
}

func atof(s string) float64 {
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// ParseGPUs parses the query-gpu CSV (no header, no units).
func ParseGPUs(text string) ([]GPU, error) {
	rs, err := rows(text)
	if err != nil {
		return nil, fmt.Errorf("query-gpu csv: %w", err)
	}
	var out []GPU
	for _, r := range rs {
		if len(r) < 12 {
			return nil, fmt.Errorf("query-gpu: expected 12 fields, got %d", len(r))
		}
		out = append(out, GPU{Index: atoi(r[0]), UUID: strings.TrimSpace(r[1]), Model: strings.TrimSpace(r[2]), BusID: strings.TrimSpace(r[3]), MemoryUsedMiB: atoi(r[4]), MemoryTotalMiB: atoi(r[5]), UtilizationPct: atoi(r[6]), MemUtilPct: atoi(r[7]), TemperatureC: atoi(r[8]), PowerW: atof(r[9]), EncoderSessions: atoi(r[10]), EncoderFPS: atof(r[11])})
	}
	return out, nil
}

// ParseProcesses parses the query-compute-apps CSV. The process name is reduced to its
// last path element so no argument or directory ever reaches the ledger.
func ParseProcesses(text string) ([]Process, error) {
	rs, err := rows(text)
	if err != nil {
		return nil, fmt.Errorf("query-compute-apps csv: %w", err)
	}
	var out []Process
	for _, r := range rs {
		if len(r) < 4 {
			continue
		}
		name := strings.TrimSpace(r[3])
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		out = append(out, Process{GPUUUID: strings.TrimSpace(r[0]), PID: atoi(r[1]), UsedMemoryMiB: atoi(r[2]), Name: name})
	}
	return out, nil
}

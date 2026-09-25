package nvidia

import (
	"context"
	"errors"
	"os"
	"testing"
)

type fake struct {
	gpu, apps string
	fail      bool
}

func (f fake) Run(_ context.Context, args ...string) ([]byte, error) {
	if f.fail {
		return nil, errors.New("exec: not found")
	}
	if len(args) > 0 && len(args[0]) > 12 && args[0][:12] == "--query-gpu=" {
		return []byte(f.gpu), nil
	}
	return []byte(f.apps), nil
}

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestQueryParsesGPUsAndProcesses(t *testing.T) {
	gpus, procs, err := Query(context.Background(), fake{gpu: fixture(t, "query-gpu.csv"), apps: fixture(t, "query-compute-apps.csv")})
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 || gpus[0].Model != "Quadro RTX 4000" || gpus[0].MemoryUsedMiB != 3120 || gpus[0].EncoderSessions != 3 || gpus[0].PowerW != 98.5 || gpus[1].UtilizationPct != 0 {
		t.Fatalf("gpus: %+v", gpus)
	}
	if len(procs) != 3 || procs[0].PID != 4242 || procs[0].UsedMemoryMiB != 2048 {
		t.Fatalf("procs: %+v", procs)
	}
	if procs[0].Name != "HandBrakeCLI" {
		t.Fatalf("the process name must be the binary alone, got %q", procs[0].Name)
	}
	for _, p := range procs {
		if p.Name == "" || len(p.Name) > 40 || containsAny(p.Name, " /") {
			t.Fatalf("leaky process name %q", p.Name)
		}
	}
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, r := range s {
			if r == c {
				return true
			}
		}
	}
	return false
}

func TestNotAvailableValuesBecomeZero(t *testing.T) {
	gpus, err := ParseGPUs("0, GPU-x, Tesla T4, 00000000:00:04.0, [N/A], 15360, [Not Supported], N/A, 40, [N/A], 0, [N/A]")
	if err != nil || len(gpus) != 1 || gpus[0].MemoryUsedMiB != 0 || gpus[0].MemoryTotalMiB != 15360 {
		t.Fatalf("%v %+v", err, gpus)
	}
}

func TestDriverUnavailableIsAnError(t *testing.T) {
	if _, _, err := Query(context.Background(), fake{fail: true}); err == nil {
		t.Fatal("expected an error")
	}
	if g, err := ParseGPUs(""); err != nil || len(g) != 0 {
		t.Fatalf("empty output is no GPUs, not an error: %v", err)
	}
}

// A process_name can carry the whole command line; a path in an argument must not leak
// into the ledger as if it were the binary.
func TestProcessNameIsTheBinaryEvenWhenArgumentsHoldPaths(t *testing.T) {
	ps, err := ParseProcesses("GPU-a, 1, 10, ffmpeg -i /data/clients/acme/match.mp4\nGPU-a, 2, 10, /usr/bin/HandBrakeCLI --input /mnt/secret/x.mkv\nGPU-a, 3, 10, /usr/local/bin/python3\n")
	if err != nil || len(ps) != 3 {
		t.Fatalf("%v %+v", err, ps)
	}
	for i, want := range []string{"ffmpeg", "HandBrakeCLI", "python3"} {
		if ps[i].Name != want {
			t.Fatalf("process %d: got %q, want %q", i, ps[i].Name, want)
		}
	}
}

func TestQueryFailuresAreErrorsWithWhatWasRead(t *testing.T) {
	if _, _, err := Query(context.Background(), fake{fail: true}); err == nil {
		t.Fatal("a driver that cannot be run is an error")
	}
	if _, _, err := Query(context.Background(), fake{gpu: "0, GPU-a, L4"}); err == nil {
		t.Fatal("a short query-gpu row is an error, not a GPU with zeroes")
	}
	gpus, procs, err := Query(context.Background(), fake{gpu: "0, GPU-a, L4, 00000000:01:00.0, [N/A], 23034, [Not Supported], 0, 40, 27.5, 0, 0\n"})
	if err != nil || len(gpus) != 1 || gpus[0].MemoryUsedMiB != 0 || gpus[0].PowerW != 27.5 || len(procs) != 0 {
		t.Fatalf("%v %+v %+v", err, gpus, procs)
	}
	gpus, _, err = Query(context.Background(), NewRunner("../../testdata/fake-nvidia-smi.sh"))
	if err != nil || len(gpus) != 2 {
		t.Fatalf("the exec runner against the fake driver: %v %+v", err, gpus)
	}
}

// smi answers by exact query; a query it does not know fails, as nvidia-smi does for
// a field the driver lacks.
type smi map[string]string

func (s smi) Run(_ context.Context, args ...string) ([]byte, error) {
	if out, ok := s[args[0]]; ok {
		return []byte(out), nil
	}
	return nil, errors.New(`Field "x" is not a valid field to query.`)
}

func base(t *testing.T) smi {
	return smi{"--query-gpu=" + GPUQuery: fixture(t, "query-gpu.csv"), "--query-compute-apps=" + ProcessQuery: ""}
}

func TestThermalFromTheDriverWhenItKnows(t *testing.T) {
	s := base(t)
	s["--query-gpu=index,temperature.gpu.tlimit"] = "0, 4\n1, [N/A]\n"
	s["--query-gpu=index,clocks_event_reasons.hw_thermal_slowdown,clocks_event_reasons.sw_thermal_slowdown"] = "0, Not Active, Active\n1, Not Active, Not Active\n"
	gpus, _, err := Query(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if gpus[0].ThermalMarginC == nil || *gpus[0].ThermalMarginC != 4 || gpus[1].ThermalMarginC != nil {
		t.Fatalf("margin: %+v %+v", gpus[0].ThermalMarginC, gpus[1].ThermalMarginC)
	}
	if gpus[0].ThermalSlowdown == nil || !*gpus[0].ThermalSlowdown || gpus[1].ThermalSlowdown == nil || *gpus[1].ThermalSlowdown {
		t.Fatalf("slowdown: %v %v", gpus[0].ThermalSlowdown, gpus[1].ThermalSlowdown)
	}
}

// Drivers before the rename answer only to clocks_throttle_reasons.*.
func TestThermalSlowdownUnderTheOldFieldNames(t *testing.T) {
	s := base(t)
	s["--query-gpu=index,clocks_throttle_reasons.hw_thermal_slowdown,clocks_throttle_reasons.sw_thermal_slowdown"] = "0, Active, Not Active\n1, Not Active, Not Active\n"
	gpus, _, err := Query(context.Background(), s)
	if err != nil || gpus[0].ThermalSlowdown == nil || !*gpus[0].ThermalSlowdown || gpus[0].ThermalMarginC != nil {
		t.Fatalf("%v %+v", err, gpus[0])
	}
}

// A driver that knows none of the optional fields is an ordinary driver: no error, no
// value — and an answer of the wrong shape is ignored, never misread.
func TestOptionalFieldsAbsentOrMalformed(t *testing.T) {
	gpus, _, err := Query(context.Background(), base(t))
	if err != nil || gpus[0].ThermalMarginC != nil || gpus[0].ThermalSlowdown != nil {
		t.Fatalf("%v %+v", err, gpus[0])
	}
	s := base(t)
	s["--query-gpu=index,temperature.gpu.tlimit"] = "0, GPU-fef8089b, Quadro RTX 4000\n"
	s["--query-gpu=index,clocks_event_reasons.hw_thermal_slowdown,clocks_event_reasons.sw_thermal_slowdown"] = "7, Active, Active\n"
	gpus, _, _ = Query(context.Background(), s)
	if gpus[0].ThermalMarginC != nil || gpus[0].ThermalSlowdown != nil {
		t.Fatalf("wrong shape or unknown index must be ignored: %+v", gpus[0])
	}
}

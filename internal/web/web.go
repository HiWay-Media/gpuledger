// Package web is gpuledger's own page: the ledger and the findings as HTML rendered
// on the server, and the headers that make it safe to expose. html/template escapes by
// context, so a container named like markup is text; the style is a file of its own, so
// the Content-Security-Policy allows no inline style and no script at all; the page
// refreshes with a meta tag, which is not script.
package web

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/ledger"
)

//go:embed style.css
var Style string

// CSP allows the page's own stylesheet and nothing else: no script, no frame, no form.
const CSP = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Secure wraps every endpoint: GET and HEAD only — gpuledger changes nothing, so it
// accepts nothing else — and the headers of a page meant to be read, not embedded.
func Secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", CSP)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			hd.Set("Allow", "GET, HEAD")
			http.Error(w, "gpuledger is read-only: GET and HEAD only", http.StatusMethodNotAllowed)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// StyleHandler serves the stylesheet the page links.
func StyleHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	fmt.Fprint(w, Style)
}

type gpuRow struct {
	Index                                   int
	Model, Util, Memory, Temp, State, Since string
	Enc                                     int
	Reserved, Tenants                       []string
}

type findingRow struct{ Level, Code, Where, Message string }

type nodeView struct {
	Node, At, Worst string
	Refresh         int
	GPUs            []gpuRow
	Findings        []findingRow
	Errors          []string
	Counts          map[string]int
}

var nodeTmpl = template.Must(template.New("node").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="{{.Refresh}}">
<title>gpuledger · {{.Node}}</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<header>
  <h1>gpuledger <span class="node">{{.Node}}</span></h1>
  <p class="meta"><span class="level {{.Worst}}">{{.Worst}}</span> · {{len .GPUs}} GPU(s) · read {{.At}} · refreshes every {{.Refresh}} s · <a href="ledger">JSON</a> · <a href="metrics">metrics</a></p>
</header>
<main>
{{if .Errors}}<section class="errors"><h2>Sources that could not be read</h2><ul>{{range .Errors}}<li><span class="level ERROR">ERROR</span> {{.}}</li>{{end}}</ul></section>{{end}}
<section>
<h2>GPUs</h2>
<div class="scroll"><table>
<thead><tr><th>gpu</th><th>model</th><th>util</th><th>memory</th><th>temp</th><th>enc</th><th>state</th><th>reserved by</th><th>tenants</th></tr></thead>
<tbody>
{{range .GPUs}}<tr><td>{{.Index}}</td><td>{{.Model}}</td><td class="num">{{.Util}}</td><td class="num">{{.Memory}}</td><td class="num">{{.Temp}}</td><td class="num">{{.Enc}}</td><td><span class="state {{.State}}">{{.State}}</span>{{if .Since}} <span class="since">{{.Since}}</span>{{end}}</td><td>{{range .Reserved}}<div>{{.}}</div>{{else}}<span class="none">—</span>{{end}}</td><td>{{range .Tenants}}<div>{{.}}</div>{{else}}<span class="none">—</span>{{end}}</td></tr>
{{end}}</tbody>
</table></div>
</section>
<section>
<h2>Findings</h2>
<ul class="findings">
{{range .Findings}}<li><span class="level {{.Level}}">{{.Level}}</span> <code>{{.Code}}</code> <span class="where">{{.Where}}</span> {{.Message}}</li>
{{else}}<li class="none">none</li>{{end}}</ul>
</section>
</main>
<footer>gpuledger — read-only. It changes nothing on this node.</footer>
</body>
</html>
`))

// NodePage renders one node's ledger and findings; refresh is the meta refresh.
func NodePage(l ledger.Ledger, fs []findings.Finding, refresh time.Duration) ([]byte, error) {
	v := nodeView{Node: l.Node, At: l.At.UTC().Format("2006-01-02 15:04:05Z"), Worst: string(findings.Worst(fs)), Refresh: int(refresh.Seconds()), Errors: l.Errors}
	if v.Refresh < 1 {
		v.Refresh = 1
	}
	for _, e := range l.Entries {
		st := e.State
		if st == "" {
			st = ledger.Classify(e)
		}
		row := gpuRow{Index: e.Index, Model: e.Model, Util: fmt.Sprintf("%d%%", e.UtilizationPct), Memory: fmt.Sprintf("%d/%d MiB", e.MemoryUsedMiB, e.MemoryTotalMiB), Temp: fmt.Sprintf("%d °C", e.TemperatureC), Enc: e.EncoderSessions, State: string(st)}
		if e.StateSince != nil {
			row.Since = findings.Human(l.At.Sub(*e.StateSince))
		}
		for _, r := range e.Reservations {
			row.Reserved = append(row.Reserved, r.JobID+"/"+r.Task)
		}
		for _, t := range e.Tenants {
			row.Tenants = append(row.Tenants, tenant(t))
		}
		v.GPUs = append(v.GPUs, row)
	}
	for _, f := range fs {
		where := f.GPU
		if where == "" {
			where = f.Node
		}
		v.Findings = append(v.Findings, findingRow{Level: string(f.Level), Code: f.Code, Where: where, Message: f.Message})
	}
	var b bytes.Buffer
	err := nodeTmpl.Execute(&b, v)
	return b.Bytes(), err
}

// tenant is how the table names a holder: the Nomad job and task, or the container,
// or the binaries — never an argument, an image or a path.
func tenant(t ledger.Tenant) string {
	name := t.Container
	if t.Kind == ledger.KindNomad && t.JobName != "" {
		name = t.JobName + "/" + t.TaskName
	}
	if name == "" {
		name = strings.Join(t.Processes, ",")
	}
	mark := ""
	switch {
	case t.Kind == ledger.KindNomad && !t.Reserved:
		mark = " · not reserved"
	case t.Kind != ledger.KindNomad:
		mark = " · outside Nomad"
	}
	return fmt.Sprintf("%s: %s, %d MiB%s", t.Kind, name, t.UsedMemoryMiB, mark)
}

type fleetNodeRow struct {
	Node, URL, Memory, Err                              string
	GPUs, Held, ReservedIdle, Unaccounted, Free, Schema int
	OtherSchema                                         bool
}

type fleetView struct {
	At, Worst string
	Refresh   int
	Nodes     []fleetNodeRow
	Total     fleetNodeRow
	Unread    int
	Jobs      []fleet.JobSummary
	Findings  []findingRow
}

var fleetTmpl = template.Must(template.New("fleet").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="{{.Refresh}}">
<title>gpuledger · fleet</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<header>
  <h1>gpuledger <span class="node">fleet</span></h1>
  <p class="meta"><span class="level {{.Worst}}">{{.Worst}}</span> · {{len .Nodes}} node(s){{if .Unread}}, {{.Unread}} unreachable{{end}} · {{.Total.GPUs}} GPU(s) · read {{.At}} · refreshes every {{.Refresh}} s · <a href="fleet">JSON</a> · <a href="metrics">metrics</a></p>
</header>
<main>
<section>
<h2>Nodes</h2>
<div class="scroll"><table>
<thead><tr><th>node</th><th>gpus</th><th>held</th><th>reserved-idle</th><th>unaccounted</th><th>free</th><th>memory</th></tr></thead>
<tbody>
{{range .Nodes}}<tr><td>{{if .URL}}<a href="{{.URL}}/">{{.Node}}</a>{{else}}{{.Node}}{{end}}{{if .OtherSchema}} <span class="since">schema {{.Schema}}</span>{{end}}</td>{{if .Err}}<td colspan="6"><span class="level ERROR">unreachable</span> {{.Err}}</td>{{else}}<td class="num">{{.GPUs}}</td><td class="num">{{.Held}}</td><td class="num">{{.ReservedIdle}}</td><td class="num">{{.Unaccounted}}</td><td class="num">{{.Free}}</td><td class="num">{{.Memory}}</td>{{end}}</tr>
{{end}}<tr class="total"><td>fleet</td><td class="num">{{.Total.GPUs}}</td><td class="num">{{.Total.Held}}</td><td class="num">{{.Total.ReservedIdle}}</td><td class="num">{{.Total.Unaccounted}}</td><td class="num">{{.Total.Free}}</td><td class="num">{{.Total.Memory}}</td></tr>
</tbody>
</table></div>
</section>
{{if .Jobs}}<section>
<h2>Jobs</h2>
<div class="scroll"><table>
<thead><tr><th>job</th><th>reserved</th><th>held</th></tr></thead>
<tbody>
{{range .Jobs}}<tr><td>{{.Namespace}}/{{.Job}}</td><td class="num">{{.Reserved}}</td><td class="num">{{.Held}}</td></tr>
{{end}}</tbody>
</table></div>
</section>{{end}}
<section>
<h2>Findings</h2>
<ul class="findings">
{{range .Findings}}<li><span class="level {{.Level}}">{{.Level}}</span> <code>{{.Code}}</code> <span class="where">{{.Where}}</span> {{.Message}}</li>
{{else}}<li class="none">none</li>{{end}}</ul>
</section>
</main>
<footer>gpuledger — read-only. It reads each node's /ledger and changes nothing.</footer>
</body>
</html>
`))

// FleetPage renders the fleet: nodes (each linked to its own page when urls knows
// where it is), the total, jobs and the fleet's findings.
func FleetPage(s fleet.Summary, fs []findings.Finding, urls map[string]string, at time.Time, refresh time.Duration) ([]byte, error) {
	row := func(n fleet.NodeSummary) fleetNodeRow {
		return fleetNodeRow{Node: n.Node, URL: urls[n.Node], Err: n.Err, GPUs: n.GPUs, Held: n.Held, ReservedIdle: n.ReservedIdle, Unaccounted: n.Unaccounted, Free: n.Free, Schema: n.Schema, OtherSchema: n.Err == "" && n.Schema != ledger.Schema, Memory: fmt.Sprintf("%d/%d MiB", n.MemoryUsedMiB, n.MemoryTotalMiB)}
	}
	v := fleetView{At: at.UTC().Format("2006-01-02 15:04:05Z"), Worst: string(findings.Worst(fs)), Refresh: int(refresh.Seconds()), Total: row(s.Total), Unread: s.Total.Unreachable, Jobs: s.Jobs}
	if v.Refresh < 1 {
		v.Refresh = 1
	}
	for _, n := range s.Nodes {
		v.Nodes = append(v.Nodes, row(n))
	}
	for _, f := range fs {
		where := f.Node
		if f.GPU != "" {
			where += " " + f.GPU
		}
		v.Findings = append(v.Findings, findingRow{Level: string(f.Level), Code: f.Code, Where: where, Message: f.Message})
	}
	var b bytes.Buffer
	err := fleetTmpl.Execute(&b, v)
	return b.Bytes(), err
}

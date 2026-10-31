package report

import (
	"embed"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"pct":  func(f float64) string { return template.HTMLEscapeString(formatPct(f)) },
	"slug": Slug,
	"when": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") },
}).ParseFS(templateFS, "templates/*.html"))

func formatPct(f float64) string {
	b, _ := json.Marshal(float64(int(f*1000+0.5)) / 10)
	return string(b) + "%"
}

// WriteSite renders an index of the latest run per suite and model, a page per
// run, and a shields.io endpoint badge. html/template escapes model output.
func WriteSite(runs []*model.Run, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })

	latest := map[[2]string]*model.Run{}
	var rows []*model.Run
	for _, r := range runs {
		key := [2]string{r.Suite, r.Model}
		if _, ok := latest[key]; !ok {
			latest[key] = r
			rows = append(rows, r)
		}
		page := PagePath(r, dir)
		if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
			return err
		}
		if err := render(page, "run.html", r); err != nil {
			return err
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Suite != rows[j].Suite {
			return rows[i].Suite < rows[j].Suite
		}
		return rows[i].Model < rows[j].Model
	})
	if err := render(filepath.Join(dir, "index.html"), "index.html", map[string]any{"Latest": rows, "All": runs}); err != nil {
		return err
	}
	return writeBadge(rows, filepath.Join(dir, "badge.json"))
}

func render(path, name string, data any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := templates.ExecuteTemplate(f, name, data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeBadge(latest []*model.Run, path string) error {
	var passed, total int
	for _, r := range latest {
		passed += r.Summary.Passed
		total += r.Summary.Total
	}
	rate, color := 0.0, "lightgrey"
	if total > 0 {
		rate = float64(passed) / float64(total)
		switch {
		case rate >= 0.9:
			color = "brightgreen"
		case rate >= 0.7:
			color = "yellow"
		default:
			color = "red"
		}
	}
	data, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "label": "evals", "message": formatPct(rate), "color": color,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

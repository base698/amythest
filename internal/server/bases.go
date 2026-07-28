package server

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/base698/amythest/internal/bases"
)

var baseBlockRe = regexp.MustCompile(`<div class="base-block" data-base-src="([A-Za-z0-9+/=]*)"></div>`)

// expandBaseBlocks substitutes inline ```base``` placeholders with rendered
// first views.
func (s *Server) expandBaseBlocks(html string) string {
	if !strings.Contains(html, `class="base-block"`) {
		return html
	}
	rows, err := s.db.AllRows()
	if err != nil {
		return html
	}
	return baseBlockRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := baseBlockRe.FindStringSubmatch(m)
		raw, err := base64.StdEncoding.DecodeString(sub[1])
		if err != nil {
			return m
		}
		b, err := bases.ParseBase(raw)
		if err != nil {
			return baseError(err.Error())
		}
		return b.Render(rows, bases.RenderContext{BaseURL: s.base()})
	})
}

func baseError(msg string) string {
	return `<div class="callout" data-callout="warning"><div class="callout-title">Base error</div><div class="callout-content"><p>` +
		template.HTMLEscapeString(msg) + `</p></div></div>`
}

// handleBases serves /bases/ (list) and /bases/{name}?view=N.
func (s *Server) handleBases(w http.ResponseWriter, r *http.Request) {
	v := s.vault.Load()
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/bases"), "/")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}

	if name == "" {
		var b strings.Builder
		names := make([]string, 0, len(v.Bases))
		for n := range v.Bases {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 {
			b.WriteString(`<p>No <code>.base</code> files in the vault yet. Create one (YAML: filters, formulas, views) and it appears here.</p>`)
		} else {
			b.WriteString(`<ul class="listing">`)
			for _, n := range names {
				b.WriteString(`<li><a class="internal" href="` + template.HTMLEscapeString(s.base()+"bases/"+n) + `">` +
					template.HTMLEscapeString(n) + `</a></li>`)
			}
			b.WriteString(`</ul>`)
		}
		s.renderPage(w, pageData{
			SiteName: s.cfg.SiteName, Base: s.base(), Title: "Bases",
			HTML: template.HTML(b.String()), Tree: s.tree.Load(),
			Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}},
		})
		return
	}

	rel, ok := v.Bases[name]
	if !ok {
		s.notFound(w, r)
		return
	}
	abs, ok := v.AssetPath(rel)
	if !ok {
		s.notFound(w, r)
		return
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, err := bases.ParseBase(raw)
	var html string
	if err != nil {
		html = baseError(err.Error())
	} else {
		rows, rerr := s.db.AllRows()
		if rerr != nil {
			http.Error(w, rerr.Error(), http.StatusInternalServerError)
			return
		}
		viewIdx, _ := strconv.Atoi(r.URL.Query().Get("view"))
		html = b.Render(rows, bases.RenderContext{
			BaseURL: s.base(),
			SelfURL: s.base() + "bases/" + name,
			ViewIdx: viewIdx,
		})
	}

	s.renderPage(w, pageData{
		SiteName: s.cfg.SiteName, Base: s.base(),
		Title: strings.TrimSuffix(filepath.Base(rel), ".base"),
		HTML:  template.HTML(html), Tree: s.tree.Load(),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}, {Name: "Bases", URL: s.base() + "bases/"}},
		Slug:        "bases/" + name,
	})
}

// ---- catalog (sqlite tabular data) ----

// handleDB serves /db (table list) and /db/{table} (browse + query box).
func (s *Server) handleDB(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		s.notFound(w, r)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/db"), "/")

	var b strings.Builder
	title := "Data"
	if name == "" {
		tables, err := s.catalog.Tables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(tables) == 0 {
			b.WriteString(`<p>No catalog tables yet. Import one with <code>amythest import csv file.csv</code>.</p>`)
		} else {
			names := make([]string, 0, len(tables))
			for t := range tables {
				names = append(names, t)
			}
			sort.Strings(names)
			b.WriteString(`<ul class="listing">`)
			for _, t := range names {
				b.WriteString(`<li><a class="internal" href="` + template.HTMLEscapeString(s.base()+"db/"+t) + `">` +
					template.HTMLEscapeString(t) + `</a> <span class="count">` + strconv.Itoa(tables[t]) + ` rows</span></li>`)
			}
			b.WriteString(`</ul>`)
		}
	} else {
		title = name
		query := r.URL.Query().Get("sql")
		if query == "" {
			query = `SELECT * FROM "` + name + `" LIMIT 100`
		}
		b.WriteString(`<form class="db-query" method="GET" action="` + template.HTMLEscapeString(s.base()+"db/"+name) + `">`)
		b.WriteString(`<textarea name="sql" rows="3" spellcheck="false">` + template.HTMLEscapeString(query) + `</textarea>`)
		b.WriteString(`<button type="submit" class="db-run">Run</button></form>`)

		cols, rows, err := s.catalog.Query(query, 500)
		if err != nil {
			b.WriteString(baseError(err.Error()))
		} else {
			b.WriteString(`<div class="base-table-wrap"><table class="base-table"><thead><tr>`)
			for _, c := range cols {
				b.WriteString(`<th>` + template.HTMLEscapeString(c) + `</th>`)
			}
			b.WriteString(`</tr></thead><tbody>`)
			for _, row := range rows {
				b.WriteString(`<tr>`)
				for _, cell := range row {
					b.WriteString(`<td>` + template.HTMLEscapeString(cell) + `</td>`)
				}
				b.WriteString(`</tr>`)
			}
			b.WriteString(`</tbody></table></div>`)
			b.WriteString(`<p class="muted">` + strconv.Itoa(len(rows)) + ` rows</p>`)
		}
	}

	s.renderPage(w, pageData{
		SiteName: s.cfg.SiteName, Base: s.base(), Title: title,
		HTML: template.HTML(b.String()), Tree: s.tree.Load(),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}, {Name: "Data", URL: s.base() + "db"}},
	})
}

// handleDBQueryAPI runs a SELECT against the catalog: POST {"sql": "..."}.
func (s *Server) handleDBQueryAPI(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		http.Error(w, "catalog not configured", http.StatusNotFound)
		return
	}
	var req struct {
		SQL     string `json:"sql"`
		MaxRows int    `json:"maxRows"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cols, rows, err := s.catalog.Query(req.SQL, req.MaxRows)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, map[string]any{"columns": cols, "rows": rows})
}

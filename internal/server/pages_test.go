package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenderPageStatusBuffersTemplateBeforeWriting(t *testing.T) {
	tmpl := template.Must(template.New("base.html").Parse(`{{define "base.html"}}{{.Title}}{{end}}`))
	s := &Server{tmpl: tmpl}
	rec := httptest.NewRecorder()

	s.renderPageStatus(rec, http.StatusNotFound, pageData{Title: "missing"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Body.String(); got != "missing" {
		t.Fatalf("body = %q, want %q", got, "missing")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestRenderPageStatusReturnsCleanTemplateError(t *testing.T) {
	tmpl := template.Must(template.New("base.html").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New("boom") },
	}).Parse(`{{define "base.html"}}prefix{{fail}}{{end}}`))
	s := &Server{tmpl: tmpl}
	rec := httptest.NewRecorder()

	s.renderPageStatus(rec, http.StatusNotFound, pageData{})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Body.String(); got == "prefix" {
		t.Fatalf("partial template output leaked: %q", got)
	}
}

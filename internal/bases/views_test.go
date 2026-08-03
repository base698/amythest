package bases

import (
	"strings"
	"testing"
)

const libraryBase = `
filters:
  and:
    - file.inFolder("Library")
formulas:
  long_read: if(note.pages > 400, "yes", "")
properties:
  file.name:
    displayName: Book
  note.author:
    displayName: Author
  note.pages:
    displayName: Pages
  note.rating:
    displayName: Rating
views:
  - type: table
    name: All books
    order: [file.name, note.author, formula.long_read]
`

func libraryRows() []*Row {
	return []*Row{
		{Slug: "Library/Dune", Path: "Library/Dune.md", Title: "Dune",
			Frontmatter: map[string]any{"author": "Herbert", "pages": 412, "rating": 5}},
		{Slug: "Library/Sphere", Path: "Library/Sphere.md", Title: "Sphere",
			Frontmatter: map[string]any{"author": "Crichton", "pages": nil, "rating": 4}},
	}
}

func TestNullComparisonInFormulaRendersBlank(t *testing.T) {
	b, err := ParseBase([]byte(libraryBase))
	if err != nil {
		t.Fatal(err)
	}
	data, err := b.Data(libraryRows(), 0)
	if err != nil {
		t.Fatal(err)
	}
	rows := data.Groups[0].Rows
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	for _, r := range rows {
		long := r[2]
		switch r[0] {
		case "Dune":
			if long != "yes" {
				t.Errorf("Dune long_read = %q, want yes", long)
			}
		case "Sphere":
			if long != "" {
				t.Errorf("Sphere long_read = %q, want blank (pages is null)", long)
			}
		}
	}
}

func TestRenderColumnOverrideAndPicker(t *testing.T) {
	b, err := ParseBase([]byte(libraryBase))
	if err != nil {
		t.Fatal(err)
	}
	ctx := RenderContext{BaseURL: "/", SelfURL: "/bases/Library", Picker: true,
		Cols: []string{"file.name", "note.pages", "note.rating"}}
	html := b.Render(libraryRows(), ctx)

	for _, want := range []string{
		`data-ref="note.pages"`, `data-ref="note.rating"`,
		`<th data-ref="note.pages">Pages</th>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in rendered table", want)
		}
	}
	if strings.Contains(html, `data-ref="note.author"`) {
		t.Error("override should drop note.author column")
	}

	// picker lists every known ref, with overridden columns checked
	for _, want := range []string{
		`class="base-cols"`,
		`value="note.author"> Author`,
		`value="note.rating" checked> Rating`,
		`value="formula.long_read"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in column picker", want)
		}
	}

	// without Picker, no form is rendered (inline base blocks)
	plain := b.Render(libraryRows(), RenderContext{BaseURL: "/", SelfURL: "/bases/Library"})
	if strings.Contains(plain, "base-cols") {
		t.Error("picker rendered without Picker flag")
	}
}

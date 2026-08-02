package httpapi

import "testing"

func TestRewriteIndexBase(t *testing.T) {
	index := `<link rel="stylesheet" href="/kanban/main.css" /><script type="module" src="/kanban/main.js"></script>`
	for _, tc := range []struct {
		name, prefix, want string
	}{
		{"no proxy header keeps absolute mount", "", index, },
		{"notes prefix nests the mount", "/notes",
			`<link rel="stylesheet" href="/notes/kanban/main.css" /><script type="module" src="/notes/kanban/main.js"></script>`},
		{"prefix already at the mount is identity", "/kanban", index},
		{"trailing slash tolerated", "/notes/",
			`<link rel="stylesheet" href="/notes/kanban/main.css" /><script type="module" src="/notes/kanban/main.js"></script>`},
		{"hostile header ignored", `/"><script>`, index},
		{"relative header ignored", "notes", index},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(rewriteIndexBase([]byte(index), tc.prefix)); got != tc.want {
				t.Fatalf("prefix %q:\ngot  %s\nwant %s", tc.prefix, got, tc.want)
			}
		})
	}
}

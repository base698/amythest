// Package vault is the only package that reads note files from disk. It
// walks the vault, extracts metadata, and provides Obsidian-style link
// resolution over the scanned set.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Note struct {
	Path   string // vault-relative, forward slashes
	Slug   string
	Title  string
	FM     Frontmatter
	MTime  time.Time
	Size   int64
	Hash   string   // sha256 of the file contents, for change detection
	Embeds []string // transclusion targets (![[...]]), for dependency fingerprints
}

// Vault is an immutable snapshot of the vault's file set with resolution
// indexes. Rebuilt by rescans; readers swap the whole snapshot atomically.
type Vault struct {
	Root   string // resolved (symlink-free) absolute root
	Notes  []*Note
	Assets []string          // vault-relative paths of non-markdown files
	Bases  map[string]string // base name (slug of path sans ext) -> rel path

	bySlug  map[string]*Note   // exact slug
	byPath  map[string]*Note   // lower(rel path without .md)
	byBase  map[string][]*Note // lower(basename without .md)
	byAlias map[string][]*Note // lower(frontmatter alias)
	asset   map[string]string  // lower(rel path) and lower(basename) -> rel path
}

// ignored reports whether a directory entry should be skipped. Dotfiles
// cover .obsidian, .trash, .git, and iCloud placeholder artifacts.
func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Scan walks root (following a symlinked root) and builds a snapshot.
// Unreadable files are skipped — iCloud can evict content at any time — and
// will be picked up by a later rescan.
func Scan(root string) (*Vault, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	v := &Vault{
		Root:    resolved,
		Bases:   map[string]string{},
		bySlug:  map[string]*Note{},
		byPath:  map[string]*Note{},
		byBase:  map[string][]*Note{},
		byAlias: map[string][]*Note{},
		asset:   map[string]string{},
	}

	err = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		name := d.Name()
		if d.IsDir() {
			if path != resolved && ignored(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored(name) {
			return nil
		}
		rel, err := filepath.Rel(resolved, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(filepath.Ext(name), ".md") {
			v.addNote(path, rel, d)
		} else if strings.EqualFold(filepath.Ext(name), ".base") {
			v.Bases[Slugify(strings.TrimSuffix(rel, ".base"))] = rel
		} else {
			v.Assets = append(v.Assets, rel)
			v.asset[strings.ToLower(rel)] = rel
			if _, exists := v.asset[strings.ToLower(name)]; !exists {
				v.asset[strings.ToLower(name)] = rel
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	v.buildIndexes()
	return v, nil
}

func (v *Vault) addNote(abs, rel string, d fs.DirEntry) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return
	}
	fm, _ := ParseFrontmatter(src)
	info, err := d.Info()
	if err != nil {
		return
	}
	title := fm.MetaString("title")
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(rel), ".md")
	}
	sum := sha256.Sum256(src)
	v.Notes = append(v.Notes, &Note{
		Path:   rel,
		Slug:   Slugify(rel),
		Title:  title,
		FM:     fm,
		MTime:  info.ModTime(),
		Size:   info.Size(),
		Hash:   hex.EncodeToString(sum[:]),
		Embeds: extractEmbeds(src),
	})
}

var embedRe = regexp.MustCompile(`!\[\[([^\[\]]+)\]\]`)

// extractEmbeds collects a note's transclusion targets so the indexer can
// fingerprint the note against the notes it inlines. A false positive (e.g.
// inside a code fence) only costs a spurious re-render, never a stale one.
func extractEmbeds(src []byte) []string {
	matches := embedRe.FindAllSubmatch(src, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		target := string(m[1])
		if i := strings.IndexAny(target, "|#"); i >= 0 {
			target = target[:i]
		}
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

func (v *Vault) buildIndexes() {
	sort.Slice(v.Notes, func(i, j int) bool { return v.Notes[i].Path < v.Notes[j].Path })
	for _, n := range v.Notes {
		// Slugs collide ("Foo Bar.md" vs "Foo-Bar.md"): the path-sorted later
		// note wins deterministically, but surface it — one note is shadowed.
		if prev, ok := v.bySlug[n.Slug]; ok {
			slog.Warn("vault slug collision", "slug", n.Slug, "shadowed", prev.Path, "wins", n.Path)
		}
		v.bySlug[n.Slug] = n
		v.byPath[strings.ToLower(strings.TrimSuffix(n.Path, ".md"))] = n
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(n.Path), ".md"))
		v.byBase[base] = append(v.byBase[base], n)
		for _, a := range n.FM.MetaStrings("aliases") {
			v.byAlias[strings.ToLower(a)] = append(v.byAlias[strings.ToLower(a)], n)
		}
	}
}

// BySlug returns the note with the exact slug.
func (v *Vault) BySlug(slug string) (*Note, bool) {
	n, ok := v.bySlug[slug]
	return n, ok
}

// Resolve resolves a wikilink target (without #fragment or |alias parts)
// using Obsidian's semantics: exact vault path first, then unique-enough
// basename (shortest path wins ties deterministically), then frontmatter
// alias.
func (v *Vault) Resolve(target string) (*Note, bool) {
	t := strings.ToLower(strings.TrimSpace(target))
	t = strings.TrimSuffix(t, ".md")
	if t == "" {
		return nil, false
	}
	if n, ok := v.byPath[t]; ok {
		return n, true
	}
	if ns := v.byBase[t]; len(ns) > 0 {
		return shortest(ns), true
	}
	if ns := v.byAlias[t]; len(ns) > 0 {
		return shortest(ns), true
	}
	return nil, false
}

// ResolveAsset resolves an embed target like "img.png" or
// "Assets/Images/img.png" to a vault-relative asset path.
func (v *Vault) ResolveAsset(target string) (string, bool) {
	rel, ok := v.asset[strings.ToLower(strings.TrimSpace(target))]
	return rel, ok
}

// AssetPath maps a vault-relative asset path back to an absolute path,
// guarding against traversal outside the vault root.
func (v *Vault) AssetPath(rel string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false
	}
	return filepath.Join(v.Root, clean), true
}

// HasAsset reports whether rel names exactly a file recorded by the scan, so
// serving can refuse everything the scan deliberately skipped (.git,
// .obsidian, dotfiles, lock files).
func (v *Vault) HasAsset(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	got, ok := v.asset[strings.ToLower(clean)]
	return ok && got == clean
}

// ReadBody reads a note's markdown body (frontmatter stripped).
func (v *Vault) ReadBody(n *Note) ([]byte, error) {
	src, err := os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(n.Path)))
	if err != nil {
		return nil, err
	}
	_, body := ParseFrontmatter(src)
	return body, nil
}

func shortest(ns []*Note) *Note {
	best := ns[0]
	for _, n := range ns[1:] {
		if len(n.Path) < len(best.Path) || (len(n.Path) == len(best.Path) && n.Path < best.Path) {
			best = n
		}
	}
	return best
}

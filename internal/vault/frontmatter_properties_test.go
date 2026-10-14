package vault

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFrontmatterPropertiesAreTypedJSONSafeAndSorted(t *testing.T) {
	fm, _ := ParseFrontmatter([]byte("---\nzeta: 2\ntasks: false\nnested:\n  html: <script>alert(1)</script>\nalpha: [one, 2]\n---\nbody\n"))
	properties := fm.Properties()
	if len(properties) != 4 {
		t.Fatalf("properties = %#v", properties)
	}
	wantKeys := []string{"alpha", "nested", "tasks", "zeta"}
	wantTypes := []string{"list", "object", "bool", "number"}
	for i := range wantKeys {
		if properties[i].Key != wantKeys[i] || properties[i].Type != wantTypes[i] {
			t.Fatalf("property %d = %#v", i, properties[i])
		}
	}
	if properties[2].Value != false {
		t.Fatalf("tasks value = %#v", properties[2].Value)
	}
	if _, err := json.Marshal(properties); err != nil {
		t.Fatalf("properties are not JSON-safe: %v", err)
	}
}

func TestFrontmatterPropertiesEmptyIsNil(t *testing.T) {
	fm, _ := ParseFrontmatter([]byte("# no frontmatter\n"))
	if properties := fm.Properties(); properties != nil {
		t.Fatalf("properties = %#v, want nil", properties)
	}
}

func TestFrontmatterPropertiesMakeSpecialFloatsJSONSafeRecursively(t *testing.T) {
	fm, _ := ParseFrontmatter([]byte("---\ntop: .nan\nnested:\n  values: [.inf, -.inf, 1.5]\n---\n"))
	properties := fm.Properties()
	if _, err := json.Marshal(properties); err != nil {
		t.Fatalf("special floats are not JSON-safe: %v", err)
	}
	if properties[1].Type != "number" || properties[1].Value != ".nan" || properties[1].Text != ".nan" {
		t.Fatalf("top special float = %#v", properties[1])
	}
	wantNested := map[string]any{"values": []any{".inf", "-.inf", 1.5}}
	if !reflect.DeepEqual(properties[0].Value, wantNested) {
		t.Fatalf("nested special floats = %#v, want %#v", properties[0].Value, wantNested)
	}
}

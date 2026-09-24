package migrate

import (
	"reflect"
	"testing"
)

func TestParseTypes_CanonicalAndLiveRenderings(t *testing.T) {
	canonical := "name: string .\ntype Doc {\nname\nsize\n}\ntype Empty {\n}\n"
	live := "name: string .\ntype Doc {\n\tsize\n\tname\n}\ntype Empty {\n}\n"
	want := map[string][]string{"Doc": {"name", "size"}, "Empty": nil}

	for label, s := range map[string]string{"canonical": canonical, "live": live} {
		if got := parseTypes(s); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: parseTypes = %v, want %v", label, got, want)
		}
	}
}

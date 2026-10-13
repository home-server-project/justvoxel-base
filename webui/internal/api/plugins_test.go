package api

import (
	"net/url"
	"testing"
)

func TestPluginDiscoverExplicitSourcesAndCursors(t *testing.T) {
	for _, raw := range []string{"", "modrinth=true&hangar=false", "modrinth=false&hangar=true", "modrinth=true&hangar=true", "modrinth=false&hangar=false", "modrinth_offset=0&hangar_offset=10000&hangar_first=true&limit=12"} {
		q, _ := url.ParseQuery(raw)
		if err := ValidatePluginDiscoverQuery(q); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"modrinth=", "hangar=0", "hangar=True", "modrinth=true&modrinth=false", "hangar=true&hangar=true", "source=hangar", "source=unknown", "provider=Modrinth", "offset=0", "hangar_first=1", "hangar_first=", "hangar_offset=10001", "modrinth_offset=-1", "hangar_offset=%2B1", "modrinth_offset=", "hangar_offset=1&hangar_offset=2", "limit=0", "limit=13", "limit=1.2", "q=a&q=b", "compatible_only=", "release_only=maybe"} {
		q, _ := url.ParseQuery(raw)
		if err := ValidatePluginDiscoverQuery(q); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

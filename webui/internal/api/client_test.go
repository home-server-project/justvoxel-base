package api

import "testing"

func TestMinecraftActionPathAllowlist(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		path, err := minecraftActionPath(action)
		if err != nil {
			t.Fatalf("%s rejected: %v", action, err)
		}
		if path != "/v1/minecraft/"+action {
			t.Fatalf("unexpected path for %s: %s", action, path)
		}
	}
	for _, action := range []string{"shell", "restart/../../shell", "", "poweroff"} {
		if _, err := minecraftActionPath(action); err == nil {
			t.Fatalf("unsupported action %q was accepted", action)
		}
	}
}

package broker

import "testing"

func TestDelegationNeverEscapesExplicitPhysicalRoots(t *testing.T) {
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/opt")
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS", `["/home","/opt"]`)
	b := &Broker{}
	for _, root := range []string{"/srv", "/etc", "/home2", "/opt2", "/"} {
		if _, _, err := b.normalizeDelegatedRoot(root, "read"); err == nil {
			t.Fatalf("root %q must be outside the physical ceiling", root)
		}
	}
}

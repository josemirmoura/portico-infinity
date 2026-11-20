package policy

import "testing"

func TestExplicitMultiRootScope(t *testing.T) {
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/opt")
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS", `["/home","/opt"]`)
	roots, err := PhysicalRoots()
	if err != nil || len(roots) != 2 || roots[0] != "/home" || roots[1] != "/opt" {
		t.Fatalf("explicit physical roots: %v, %v", roots, err)
	}
	cfg := &Config{}
	cfg.Filesystem.Read = []string{"/home/josemir", "/opt"}
	cfg.Filesystem.Write = []string{"/home", "/opt/project"}
	cfg.Shell.CWDRoots = []string{"/opt"}
	if err := cfg.ValidatePhysicalRoots(roots, false); err != nil { t.Fatal(err) }
	for _, forbidden := range []string{"/srv", "/home2", "/opt2", "/etc", "/"} {
		cfg.Filesystem.Write = []string{forbidden}
		if err := cfg.ValidatePhysicalRoots(roots, false); err == nil {
			t.Fatalf("%q escaped multi-root ceiling", forbidden)
		}
	}
}

func TestMultiRootInvalidConfigurationFailsClosed(t *testing.T) {
	for _, invalid := range []string{
		"", "[]", "not-json", `["/"]`, `["/opt","/opt"]`,
		`["/opt","/opt/project"]`, `["/home","/srv"]`, `["/home/../srv","/opt"]`,
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS", invalid)
			roots, err := PhysicalRoots()
			if invalid == `["/home","/srv"]` {
				if err != nil { t.Fatal(err) }
				cfg := &Config{Filesystem: FilesystemPolicy{Write: []string{"/opt"}}}
				if cfg.ValidatePhysicalRoots(roots, false) == nil {
					t.Fatal("policy outside configured ceilings was allowed")
				}
				return
			}
			if err == nil { t.Fatalf("invalid ceiling accepted: %q", invalid) }
		})
	}
}

package sensitive

import "testing"

func TestHomeIdentityFilesRequireSeparateAuthorization(t *testing.T) {
	for _, path := range []string{
		"/home/josemir/.ssh/id_ed25519", "/home/josemir/.ssh/authorized_keys",
		"/home/josemir/.gnupg/private-keys-v1.d/key", "/home/josemir/.aws/credentials",
		"/home/josemir/.config/gh/hosts.yml", "/home/josemir/.bashrc",
		"/opt/project/.env", "/opt/project/private.pem",
	} {
		if !ProtectedName(path) { t.Fatalf("secret %q was not protected", path) }
	}
	for _, path := range []string{
		"/home/josemir/projetos/app.go", "/opt/project/README.md", "/opt/project/.env.example",
	} {
		if ProtectedName(path) { t.Fatalf("ordinary file %q was incorrectly protected", path) }
	}
}

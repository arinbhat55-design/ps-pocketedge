package api

import (
	"strings"
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
)

func TestGeneratedGitCompose(t *testing.T) {
	content, dockerfilePath, err := generatedGitCompose("apps/web", "docker/Dockerfile", 8080, []string{"API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if dockerfilePath != "apps/web/docker/Dockerfile" {
		t.Fatalf("Dockerfile path = %q", dockerfilePath)
	}
	if !compose.Parse(content).Valid || !compose.HasBuild(content) || !strings.Contains(content, `"8080:8080"`) || !strings.Contains(content, `API_KEY: "${API_KEY}"`) {
		t.Fatalf("invalid generated Compose file: %s", content)
	}
}

func TestGeneratedGitComposeRejectsEscapes(t *testing.T) {
	for _, tc := range []struct{ context, dockerfile string }{
		{"../private", "Dockerfile"},
		{".", "../Dockerfile"},
		{"https://example.com/repo.git", "Dockerfile"},
		{"/absolute", "Dockerfile"},
	} {
		if _, _, err := generatedGitCompose(tc.context, tc.dockerfile, 8080, nil); err == nil {
			t.Errorf("accepted context %q, Dockerfile %q", tc.context, tc.dockerfile)
		}
	}
}

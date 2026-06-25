package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRuntimeManifestsStartHermesRuntime(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, manifest := range deploymentRuntimeManifests(repoRoot) {
		t.Run(manifest, func(t *testing.T) {
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			pattern := regexp.MustCompile(`(?s)name:\s+hermes-runtime.*?spec:\s+replicas:\s+([0-9]+)`)
			matches := pattern.FindSubmatch(raw)
			if len(matches) != 2 {
				t.Fatalf("could not find hermes-runtime replicas in %s", manifest)
			}
			if string(matches[1]) != "1" {
				t.Fatalf("expected hermes-runtime replicas 1 in %s, got %s", manifest, matches[1])
			}
		})
	}
}

func TestRuntimeManifestsSeedLiteDefaultImages(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, manifest := range append(deploymentRuntimeManifests(repoRoot), filepath.Join(repoRoot, "backend", "deployments", "k8s", "clawreef-incluster.yaml")) {
		t.Run(manifest, func(t *testing.T) {
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			for _, image := range []string{
				"ghcr.io/yuan-lab-llm/agentsruntime/openclaw-lite:latest",
				"ghcr.io/yuan-lab-llm/agentsruntime/hermes-lite:latest",
			} {
				if !strings.Contains(string(raw), image) {
					t.Fatalf("manifest %s must seed lite image %s", manifest, image)
				}
			}
		})
	}
}

func TestRuntimeManifestsExposeOpenClawGatewayOnPodIP(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, manifest := range deploymentRuntimeManifests(repoRoot) {
		t.Run(manifest, func(t *testing.T) {
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			text := string(raw)
			want := "/usr/local/bin/openclaw gateway run --allow-unconfigured --auth token --bind lan --force"
			if !strings.Contains(text, want) {
				t.Fatalf("manifest %s must expose OpenClaw gateway on the pod network with %q", manifest, want)
			}
			if strings.Contains(text, "--auth token --bind auto --force") {
				t.Fatalf("manifest %s must not use OpenClaw --bind auto because it can bind to loopback inside runtime pods", manifest)
			}
		})
	}
}

func deploymentRuntimeManifests(repoRoot string) []string {
	return []string{
		filepath.Join(repoRoot, "deployments", "k8s", "cluster", "clawmanager.yaml"),
		filepath.Join(repoRoot, "deployments", "k8s", "single-node", "clawmanager.yaml"),
		filepath.Join(repoRoot, "deployments", "k3s", "cluster", "clawmanager.yaml"),
		filepath.Join(repoRoot, "deployments", "k3s", "single-node", "clawmanager.yaml"),
	}
}

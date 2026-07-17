package services

import "testing"

func TestNormalizeV2RuntimeTypeAcceptsManagedRuntimeTypes(t *testing.T) {
	for _, input := range []string{"openclaw", " OpenClaw ", "hermes", "Hermes"} {
		got, ok := NormalizeV2RuntimeType(input)
		if !ok {
			t.Fatalf("expected %q to be accepted", input)
		}
		if got != RuntimeTypeOpenClaw && got != RuntimeTypeHermes {
			t.Fatalf("expected normalized managed runtime type, got %q", got)
		}
	}
}

func TestNormalizeV2RuntimeTypeRejectsLegacyRuntimeTypes(t *testing.T) {
	for _, input := range []string{"webtop", "ubuntu", "", "desktop", "shell"} {
		if got, ok := NormalizeV2RuntimeType(input); ok {
			t.Fatalf("expected %q to be rejected, got %q", input, got)
		}
	}
}

func TestRuntimeWorkspacePath(t *testing.T) {
	got := RuntimeWorkspacePath("openclaw", 45, 123)
	want := "/workspaces/openclaw/user-45/instance-123"
	if got != want {
		t.Fatalf("expected workspace path %q, got %q", want, got)
	}
}

func TestRuntimeLinuxID(t *testing.T) {
	if got, want := RuntimeLinuxID(123), 200123; got != want {
		t.Fatalf("expected linux id %d, got %d", want, got)
	}
}

func TestNormalizeInstanceModeAcceptsThreeValues(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: " lite ", want: InstanceModeLite},
		{input: "Isolated", want: InstanceModeIsolated},
		{input: "Pro", want: InstanceModePro},
	} {
		got, ok := NormalizeInstanceMode(tc.input)
		if !ok || got != tc.want {
			t.Fatalf("NormalizeInstanceMode(%q) = %q/%v, want %q/true", tc.input, got, ok, tc.want)
		}
	}
}

func TestNormalizeInstanceModeRejectsUnknownValues(t *testing.T) {
	for _, input := range []string{"", "gateway", "sandbox", "dedicated"} {
		if got, ok := NormalizeInstanceMode(input); ok {
			t.Fatalf("NormalizeInstanceMode(%q) = %q/true, want rejected", input, got)
		}
	}
}

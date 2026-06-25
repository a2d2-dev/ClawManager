package services

import "testing"

func TestApplyDesktopStreamProfileEnvStandard(t *testing.T) {
	overrides := applyDesktopStreamProfileEnv(map[string]string{
		"SELKIES_ENCODER":         "x264enc",
		"SELKIES_USE_CSS_SCALING": "false",
		"SELKIES_FRAMERATE":       "10",
		"SELKIES_H264_CRF":        "20",
	}, DesktopStreamProfileStandard)

	want := map[string]string{
		"CLAWMANAGER_DESKTOP_STREAM_PROFILE": "standard",
		"SELKIES_ENCODER":                    "x264enc,jpeg",
		"SELKIES_USE_CSS_SCALING":            "true",
		"SELKIES_FRAMERATE":                  "35",
		"SELKIES_H264_CRF":                   "34",
		"SELKIES_SECOND_SCREEN":              "false",
		"SELKIES_AUDIO_ENABLED":              "false",
	}

	for key, value := range want {
		if got := overrides[key]; got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
}

func TestDesktopStreamProfileFromEnvIgnoresEncoderDetails(t *testing.T) {
	overrides := map[string]string{
		"SELKIES_ENCODER":         "x264enc,jpeg",
		"SELKIES_USE_CSS_SCALING": "true",
		"SELKIES_FRAMERATE":       "40",
		"SELKIES_H264_CRF":        "24",
	}

	if got := desktopStreamProfileFromEnv(overrides); got != DesktopStreamProfileHigh {
		t.Fatalf("desktopStreamProfileFromEnv() = %q, want %q", got, DesktopStreamProfileHigh)
	}
}

func TestEnsureDesktopStreamProfileEnvUpgradesSavedProfile(t *testing.T) {
	overrides := ensureDesktopStreamProfileEnv(map[string]string{
		"CLAWMANAGER_DESKTOP_STREAM_PROFILE": "standard",
		"SELKIES_ENCODER":                    "x264enc",
		"SELKIES_FRAMERATE":                  "35",
		"SELKIES_H264_CRF":                   "34",
	}, RuntimeBackendDesktop)

	if got := overrides["SELKIES_ENCODER"]; got != "x264enc,jpeg" {
		t.Fatalf("SELKIES_ENCODER = %q, want x264enc,jpeg", got)
	}
	if got := overrides["SELKIES_USE_CSS_SCALING"]; got != "true" {
		t.Fatalf("SELKIES_USE_CSS_SCALING = %q, want true", got)
	}
}

func TestEnsureDesktopStreamProfileEnvDefaultsDesktopOnlyWhenUnset(t *testing.T) {
	desktop := ensureDesktopStreamProfileEnv(nil, RuntimeBackendDesktop)
	if got := desktop["CLAWMANAGER_DESKTOP_STREAM_PROFILE"]; got != DesktopStreamProfileStandard {
		t.Fatalf("desktop profile = %q, want %q", got, DesktopStreamProfileStandard)
	}

	custom := ensureDesktopStreamProfileEnv(map[string]string{
		"SELKIES_ENCODER": "custom",
	}, RuntimeBackendDesktop)
	if got := custom["SELKIES_ENCODER"]; got != "custom" {
		t.Fatalf("custom encoder = %q, want custom", got)
	}

	shell := ensureDesktopStreamProfileEnv(nil, RuntimeBackendShell)
	if shell != nil {
		t.Fatalf("shell runtime should not receive desktop stream env")
	}
}

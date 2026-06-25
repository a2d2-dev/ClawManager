package services

import "strings"

const (
	DesktopStreamProfileLow      = "low"
	DesktopStreamProfileStandard = "standard"
	DesktopStreamProfileHigh     = "high"

	desktopStreamProfileEnvKey = "CLAWMANAGER_DESKTOP_STREAM_PROFILE"
)

var selkiesDesktopStreamEnvKeys = []string{
	desktopStreamProfileEnvKey,
	"SELKIES_ENCODER",
	"SELKIES_USE_CSS_SCALING",
	"SELKIES_FRAMERATE",
	"SELKIES_H264_CRF",
	"SELKIES_AUDIO_ENABLED",
	"SELKIES_SECOND_SCREEN",
}

func normalizeDesktopStreamProfile(profile string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "":
		return "", true
	case DesktopStreamProfileLow:
		return DesktopStreamProfileLow, true
	case DesktopStreamProfileStandard:
		return DesktopStreamProfileStandard, true
	case DesktopStreamProfileHigh:
		return DesktopStreamProfileHigh, true
	default:
		return "", false
	}
}

func applyDesktopStreamProfileEnv(overrides map[string]string, profile string) map[string]string {
	normalized, ok := normalizeDesktopStreamProfile(profile)
	if !ok || normalized == "" {
		return overrides
	}

	if overrides == nil {
		overrides = map[string]string{}
	}

	for _, key := range selkiesDesktopStreamEnvKeys {
		delete(overrides, key)
	}

	overrides[desktopStreamProfileEnvKey] = normalized
	overrides["SELKIES_ENCODER"] = "x264enc,jpeg"
	overrides["SELKIES_USE_CSS_SCALING"] = "true"
	overrides["SELKIES_SECOND_SCREEN"] = "false"
	overrides["SELKIES_AUDIO_ENABLED"] = "false"

	switch normalized {
	case DesktopStreamProfileLow:
		overrides["SELKIES_FRAMERATE"] = "30"
		overrides["SELKIES_H264_CRF"] = "42"
	case DesktopStreamProfileHigh:
		overrides["SELKIES_FRAMERATE"] = "40"
		overrides["SELKIES_H264_CRF"] = "24"
	default:
		overrides["SELKIES_FRAMERATE"] = "35"
		overrides["SELKIES_H264_CRF"] = "34"
	}

	return overrides
}

func ensureDesktopStreamProfileEnv(overrides map[string]string, runtimeType string) map[string]string {
	if normalizeInstanceRuntimeType(runtimeType) != RuntimeBackendDesktop {
		return overrides
	}

	profile := desktopStreamProfileFromEnv(overrides)
	if profile != "" {
		return applyDesktopStreamProfileEnv(overrides, profile)
	}

	for _, key := range selkiesDesktopStreamEnvKeys {
		if _, ok := overrides[key]; ok {
			return overrides
		}
	}

	return applyDesktopStreamProfileEnv(overrides, DesktopStreamProfileStandard)
}

func desktopStreamProfileFromEnv(overrides map[string]string) string {
	if profile, ok := overrides[desktopStreamProfileEnvKey]; ok {
		if normalized, valid := normalizeDesktopStreamProfile(profile); valid && normalized != "" {
			return normalized
		}
	}

	framerate := strings.TrimSpace(overrides["SELKIES_FRAMERATE"])
	crf := strings.TrimSpace(overrides["SELKIES_H264_CRF"])
	switch {
	case framerate == "30" && crf == "42":
		return DesktopStreamProfileLow
	case framerate == "40" && crf == "24":
		return DesktopStreamProfileHigh
	case framerate == "35" && crf == "34":
		return DesktopStreamProfileStandard
	default:
		return ""
	}
}

package services

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"path"
	"sort"
	"strings"
	"testing"
)

func TestHashDirectoryPreservesSingleTopLevelSubdirectory(t *testing.T) {
	files := map[string][]byte{
		"src/main.py": []byte("print('hello')\n"),
	}

	got := hashDirectory(files)
	want := referenceSkillContentMD5(map[string][]byte{
		"src/main.py": []byte("print('hello')\n"),
	})
	if got != want {
		t.Fatalf("hashDirectory() = %s, want %s", got, want)
	}

	flattened := referenceSkillContentMD5(map[string][]byte{
		"main.py": []byte("print('hello')\n"),
	})
	if got == flattened {
		t.Fatalf("hashDirectory stripped the skill's internal src/ directory")
	}
}

func TestExtractSkillDirectoriesStripsArchiveRootOnlyOnce(t *testing.T) {
	archive := buildTestZip(t, map[string][]byte{
		"weather/SKILL.md":    []byte("# Weather\n"),
		"weather/src/main.py": []byte("print('weather')\n"),
	})

	dirs, err := extractSkillDirectories("weather.zip", archive)
	if err != nil {
		t.Fatalf("extractSkillDirectories() error = %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("extractSkillDirectories() returned %d dirs, want 1", len(dirs))
	}
	if _, ok := dirs[0].Files["src/main.py"]; !ok {
		t.Fatalf("expected skill files to preserve src/main.py after stripping archive root once: %#v", dirs[0].Files)
	}

	got := hashDirectory(dirs[0].Files)
	want := referenceSkillContentMD5(map[string][]byte{
		"SKILL.md":    []byte("# Weather\n"),
		"src/main.py": []byte("print('weather')\n"),
	})
	if got != want {
		t.Fatalf("hashDirectory(extracted files) = %s, want %s", got, want)
	}
}

func TestExtractSkillDirectoriesAcceptsRootSkillPackage(t *testing.T) {
	archive := buildTestZip(t, map[string][]byte{
		".env":                []byte("TOKEN=example\n"),
		"SKILL.md":            []byte("# Weather\n"),
		"__MACOSX/._SKILL.md": []byte("metadata\n"),
		"scripts/run.sh":      []byte("python src/main.py\n"),
		"src/main.py":         []byte("print('weather')\n"),
	})

	dirs, err := extractSkillDirectories("weather.zip", archive)
	if err != nil {
		t.Fatalf("extractSkillDirectories() error = %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("extractSkillDirectories() returned %d dirs, want 1", len(dirs))
	}
	if dirs[0].Name != "weather" {
		t.Fatalf("root skill package name = %q, want weather", dirs[0].Name)
	}
	if _, ok := dirs[0].Files["SKILL.md"]; !ok {
		t.Fatalf("expected root SKILL.md to be preserved: %#v", dirs[0].Files)
	}
	if _, ok := dirs[0].Files["scripts/run.sh"]; !ok {
		t.Fatalf("expected root scripts/run.sh to be preserved: %#v", dirs[0].Files)
	}
	if _, ok := dirs[0].Files[".env"]; !ok {
		t.Fatalf("expected root .env to be preserved for scanning: %#v", dirs[0].Files)
	}
	if _, ok := dirs[0].Files["__MACOSX/._SKILL.md"]; ok {
		t.Fatalf("expected archive metadata to be ignored: %#v", dirs[0].Files)
	}
}

func TestExtractSkillDirectoriesImportsMultipleManifestDirs(t *testing.T) {
	archive := buildTestZip(t, map[string][]byte{
		"alpha/SKILL.md": []byte("# Alpha\n"),
		"beta/SKILL.md":  []byte("# Beta\n"),
	})

	dirs, err := extractSkillDirectories("skills.zip", archive)
	if err != nil {
		t.Fatalf("extractSkillDirectories() error = %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("extractSkillDirectories() returned %d dirs, want 2", len(dirs))
	}
	if dirs[0].Name != "alpha" || dirs[1].Name != "beta" {
		t.Fatalf("skill directory names = %q, %q; want alpha, beta", dirs[0].Name, dirs[1].Name)
	}
}

func TestExtractSkillDirectoriesRejectsTopLevelDirWithoutManifest(t *testing.T) {
	archive := buildTestZip(t, map[string][]byte{
		"weather/src/main.py": []byte("print('weather')\n"),
	})

	_, err := extractSkillDirectories("weather.zip", archive)
	if err == nil {
		t.Fatal("extractSkillDirectories() error = nil, want SKILL.md error")
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("extractSkillDirectories() error = %v, want SKILL.md error", err)
	}
}

func TestExtractSkillDirectoriesRejectsLooseFileWithoutRootManifest(t *testing.T) {
	archive := buildTestZip(t, map[string][]byte{
		"README.md":        []byte("not a skill manifest\n"),
		"weather/SKILL.md": []byte("# Weather\n"),
	})

	_, err := extractSkillDirectories("weather.zip", archive)
	if err == nil {
		t.Fatal("extractSkillDirectories() error = nil, want loose file error")
	}
	if !strings.Contains(err.Error(), "loose file README.md") {
		t.Fatalf("extractSkillDirectories() error = %v, want loose README.md error", err)
	}
}

func TestFlattenSingleTopLevelDirForArchiveRoot(t *testing.T) {
	files := map[string][]byte{
		"weather/src/main.py": []byte("print('weather')\n"),
	}

	got := hashDirectory(flattenSingleTopLevelDir(files))
	want := referenceSkillContentMD5(map[string][]byte{
		"src/main.py": []byte("print('weather')\n"),
	})
	if got != want {
		t.Fatalf("hashDirectory(flattenSingleTopLevelDir(files)) = %s, want %s", got, want)
	}
}

func buildTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry, err := writer.Create(key)
		if err != nil {
			t.Fatalf("Create(%q): %v", key, err)
		}
		if _, err := entry.Write(files[key]); err != nil {
			t.Fatalf("Write(%q): %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	return buffer.Bytes()
}

func referenceSkillContentMD5(files map[string][]byte) string {
	entryKinds := map[string]string{}
	fileMap := map[string][]byte{}
	for key, body := range files {
		clean := path.Clean(key)
		if clean == "." || clean == "" {
			continue
		}
		fileMap[clean] = body
		entryKinds[clean] = "file"
		parts := splitTestPath(clean)
		for i := 1; i < len(parts); i++ {
			entryKinds[path.Join(parts[:i]...)] = "dir"
		}
	}

	keys := make([]string, 0, len(entryKinds))
	for key := range entryKinds {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	digest := md5.New()
	for _, key := range keys {
		_, _ = digest.Write([]byte(key))
		_, _ = digest.Write([]byte("\n"))
		if entryKinds[key] == "dir" {
			_, _ = digest.Write([]byte("dir\n"))
			continue
		}
		_, _ = digest.Write([]byte("file\n"))
		_, _ = digest.Write(fileMap[key])
		_, _ = digest.Write([]byte("\n"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func splitTestPath(value string) []string {
	result := []string{}
	for _, part := range bytes.Split([]byte(value), []byte("/")) {
		if len(part) > 0 {
			result = append(result, string(part))
		}
	}
	return result
}

package model

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// fakeClient is an in-memory model.Client for tests.
type fakeClient struct {
	cmds  map[string]string // cmd -> stdout
	files map[string]string // path -> content
}

func (f *fakeClient) Run(cmd string) ([]byte, []byte, error) {
	if out, ok := f.cmds[cmd]; ok {
		return []byte(out), nil, nil
	}
	return nil, nil, fmt.Errorf("no such command: %s", cmd)
}

func (f *fakeClient) Expand(pattern string) ([]string, error) {
	var out []string
	for path := range f.files {
		ok, err := filepath.Match(pattern, path)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeClient) ReadFile(path string) ([]byte, error) {
	if out, ok := f.files[path]; ok {
		return []byte(out), nil
	}
	return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
}

func TestSpecFetcherCommands(t *testing.T) {
	f := Spec{
		Commands:     []string{"/export"},
		CommandNames: []string{"export.rsc"},
	}.Compile()
	arts, err := f.Fetch(&fakeClient{cmds: map[string]string{"/export": "config line\n"}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 1 || arts[0].Name != "export.rsc" || string(arts[0].Data) != "config line\n" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
}

func TestSpecFetcherFiles(t *testing.T) {
	f := Spec{
		Files:     []string{"/conf/config.xml"},
		FileNames: []string{"config.xml"},
	}.Compile()
	arts, err := f.Fetch(&fakeClient{files: map[string]string{"/conf/config.xml": "<config/>"}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 1 || arts[0].Name != "config.xml" || string(arts[0].Data) != "<config/>" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
}

func TestSpecFetcherErrors(t *testing.T) {
	f := Spec{Files: []string{"/missing"}}.Compile()
	if _, err := f.Fetch(&fakeClient{}); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestStripLines(t *testing.T) {
	prompt := regexp.MustCompile(`^\s*\[\S+@\S+\]\s*[>~]\s*$`)
	got := string(stripLines("[admin@switch] > \n/export output\n[admin@switch] > \n", []*regexp.Regexp{prompt}))
	want := "/export output\n"
	if got != want {
		t.Fatalf("stripLines:\n got %q\nwant %q", got, want)
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"hostname":                      "hostname.txt",
		"docker ps --format {{.Names}}": "docker-ps---format--Names.txt",
		"/export":                       "export.txt",
		"":                              "command.txt",
	}
	for cmd, want := range cases {
		if got := safeName(cmd); got != want {
			t.Errorf("safeName(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestGeneric(t *testing.T) {
	f := Generic([]string{"uptime"}, []string{"/etc/hosts"})
	arts, err := f.Fetch(&fakeClient{
		cmds:  map[string]string{"uptime": " 22:00  up 1h\n"},
		files: map[string]string{"/etc/hosts": "127.0.0.1 localhost\n"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("want 2 artifacts, got %+v", arts)
	}
	if arts[0].Name != "uptime.txt" || arts[1].Name != "hosts" {
		t.Fatalf("unexpected artifact names: %+v", arts)
	}
}

func TestGetUnknownModel(t *testing.T) {
	if _, err := Get("nope"); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("Get(nope): %v", err)
	}
}

func TestGlobFiles(t *testing.T) {
	f := Spec{Files: []string{"/etc/dnsmasq.d/*"}}.Compile()
	client := &fakeClient{files: map[string]string{
		"/etc/dnsmasq.d/02-a.conf": "a\n",
		"/etc/dnsmasq.d/05-b.conf": "b\n",
		"/etc/hosts":               "untouched\n",
	}}
	arts, err := f.Fetch(client)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("want 2 artifacts, got %+v", arts)
	}
	// Each match is a verbatim artifact named relative to the glob dir,
	// sorted for deterministic output.
	if arts[0].Name != "02-a.conf" || string(arts[0].Data) != "a\n" {
		t.Fatalf("unexpected artifact[0]: %+v", arts[0])
	}
	if arts[1].Name != "05-b.conf" || string(arts[1].Data) != "b\n" {
		t.Fatalf("unexpected artifact[1]: %+v", arts[1])
	}
}

func TestGlobEmptyMatch(t *testing.T) {
	// A glob with no matches produces no artifacts and no error.
	f := Spec{Files: []string{"/etc/dnsmasq.d/*"}}.Compile()
	arts, err := f.Fetch(&fakeClient{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 0 {
		t.Fatalf("want 0 artifacts, got %+v", arts)
	}
}

func TestMixedStaticAndGlobFiles(t *testing.T) {
	f := Spec{
		Files:       []string{"/etc/hosts", "/etc/modprobe.d/*.conf"},
		FileNames:   []string{"hosts", ""},
		SkipMissing: true,
	}.Compile()
	client := &fakeClient{files: map[string]string{
		"/etc/hosts":                         "localhost\n",
		"/etc/modprobe.d/hba-blacklist.conf": "blacklist mpt3sas\n",
	}}
	arts, err := f.Fetch(client)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(arts) != 2 || arts[0].Name != "hosts" || arts[1].Name != "hba-blacklist.conf" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
}

func TestSkipMissing(t *testing.T) {
	f := Spec{
		Files:       []string{"/exists", "/missing"},
		FileNames:   []string{"a", "b"},
		SkipMissing: true,
	}.Compile()
	arts, err := f.Fetch(&fakeClient{files: map[string]string{"/exists": "x"}})
	if err != nil {
		t.Fatalf("Fetch with SkipMissing: %v", err)
	}
	if len(arts) != 1 || arts[0].Name != "a" {
		t.Fatalf("missing file not skipped: %+v", arts)
	}

	// Without SkipMissing, the same fetch fails.
	f2 := Spec{Files: []string{"/missing"}}.Compile()
	if _, err := f2.Fetch(&fakeClient{}); err == nil {
		t.Fatal("expected error without SkipMissing")
	}
}

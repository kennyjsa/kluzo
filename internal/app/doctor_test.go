package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/config"
)

func TestDoctorHostCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kluzo")
	if _, err := config.Init(dir); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "kluzo.yaml")
	raw, _ := os.ReadFile(cfg)
	raw = []byte(strings.NewReplacer("__PROJECT_NAME__", "demo", "OWNER/REPO", "acme/demo").Replace(string(raw)))
	if err := os.WriteFile(cfg, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	none := func(context.Context, string) ([]string, error) { return nil, nil }
	keychain := func(context.Context, string) ([]string, error) { return []string{"osxkeychain"}, nil }

	out, err := Doctor{Env: []string{"HOME=" + home}, Helpers: none}.Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || len(out.Refused)+len(out.Warnings) != 0 {
		t.Errorf("clean host: %+v %v", out, err)
	}
	out, err = Doctor{Env: []string{"HOME=" + home}, Helpers: keychain}.Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || len(out.Refused) != 1 || !strings.Contains(out.Refused[0], "osxkeychain") {
		t.Errorf("helper: %+v %v", out, err)
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(string(raw), "allow_host_credentials: false", "allow_host_credentials: true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = Doctor{Env: []string{"HOME=" + home}, Helpers: keychain}.Execute(context.Background(), DoctorInput{Dir: dir})
	if err != nil || len(out.Refused) != 0 || len(out.Warnings) != 1 {
		t.Errorf("allowed: %+v %v", out, err)
	}
	if _, err := (Doctor{Env: []string{}, Helpers: none}).Execute(context.Background(), DoctorInput{Dir: dir}); err == nil {
		t.Error("no HOME is not an error")
	}
	if err := os.WriteFile(cfg, []byte("version: 1\nnope: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := (Doctor{Helpers: none}).Execute(context.Background(), DoctorInput{Dir: dir}); err != nil || len(out.Invalid) == 0 {
		t.Errorf("invalid configuration: %+v %v", out, err)
	}
}

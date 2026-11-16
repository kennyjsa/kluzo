package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCheckRoles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DefaultConfigDir)
	ctx := context.Background()

	if _, err := (InitConfig{}).Execute(ctx, InitConfigInput{Dir: dir}); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	out, err := (CheckRoles{}).Execute(ctx, CheckRolesInput{Dir: dir})
	if err != nil || len(out.Problems) != 0 {
		t.Fatalf("CheckRoles: %v, problems %v", err, out.Problems)
	}
	if !slices.IsSorted(out.Roles) || len(out.Roles) != 10 {
		t.Errorf("roles = %v, want 10 sorted names", out.Roles)
	}

	out, err = (CheckRoles{}).Execute(ctx, CheckRolesInput{Dir: filepath.Join(dir, "absent")})
	if err != nil || len(out.Problems) != 1 || len(out.Roles) != 0 {
		t.Errorf("missing config: err %v, out %+v; want one problem", err, out)
	}
}

// roles check loads the whole configuration, so a bad flow is reported too.
func TestCheckRolesReportsFlowErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DefaultConfigDir)
	ctx := context.Background()
	if _, err := (InitConfig{}).Execute(ctx, InitConfigInput{Dir: dir}); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	flow := filepath.Join(dir, "flows", "broken.yaml")
	if err := os.WriteFile(flow, []byte("not: [valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (CheckRoles{}).Execute(ctx, CheckRolesInput{Dir: dir})
	if err != nil || len(out.Problems) == 0 {
		t.Fatalf("CheckRoles: err %v, problems %v; want a flow problem", err, out.Problems)
	}
}

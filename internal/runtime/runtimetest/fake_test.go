package runtimetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

var _ runtime.Runtime = (*Fake)(nil)

func spec(id string) runtime.EnvironmentSpec {
	return runtime.EnvironmentSpec{ID: id, Hostname: "ghrm-" + id, Cores: 1, MemoryMB: 512}
}

func TestFakeLifecycle(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	ref, err := f.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.Create(ctx, spec("aaa"))
	if err != nil || again != ref {
		t.Fatalf("Create must be idempotent per ID: %v %v", again, err)
	}
	if err := f.Start(ctx, ref); err != nil {
		t.Fatal(err)
	}
	st, err := f.Status(ctx, ref)
	if err != nil || !st.Running || st.EnvironmentID != "aaa" || st.IP == "" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	list, _ := f.List(ctx)
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if err := f.Destroy(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := f.Destroy(ctx, ref); err != nil {
		t.Fatalf("Destroy of a missing environment must be nil, got %v", err)
	}
	if _, err := f.Status(ctx, ref); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Status after destroy = %v, want ErrNotFound", err)
	}
}

func TestFakeCreateErrAndValidation(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.CreateErr = errors.New("boom")
	if _, err := f.Create(ctx, spec("bbb")); err == nil {
		t.Fatal("want CreateErr")
	}
	f.CreateErr = nil
	bad := spec("ccc")
	bad.Cores = 0
	if _, err := f.Create(ctx, bad); !errors.Is(err, runtime.ErrInvalidSpec) {
		t.Fatalf("Create(invalid) = %v, want ErrInvalidSpec", err)
	}
}

func TestFakeTemplates(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	var _ runtime.Templates = f
	ref, err := f.CreateTemplate(ctx, runtime.TemplateSpec{ID: "t1", Archive: strings.NewReader("abc"), Size: 3, SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	s := spec("e1")
	s.Template = f.TemplateEnvironmentRef(ref)
	envRef, _ := f.Create(ctx, s)
	if used, _ := f.TemplateInUse(ctx, ref); !used {
		t.Fatal("template should be in use")
	}
	if err := f.DeleteTemplate(ctx, ref); !errors.Is(err, runtime.ErrTemplateInUse) {
		t.Fatalf("delete in use = %v", err)
	}
	_ = f.Destroy(ctx, envRef)
	if err := f.DeleteTemplate(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if len(f.TemplateIDs()) != 0 {
		t.Fatal("template not deleted")
	}
	f.CreateTemplateErr = errors.New("boom")
	if _, err := f.CreateTemplate(ctx, runtime.TemplateSpec{ID: "t2", Archive: strings.NewReader("x"), Size: 1, SHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("want the injected error")
	}
}

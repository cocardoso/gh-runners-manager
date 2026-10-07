package runtime

import (
	"errors"
	"testing"
)

func validSpec() EnvironmentSpec {
	return EnvironmentSpec{
		ID: "01k6abcdefghjkmnpqrstvwxyz", Hostname: "ghrm-vwxyz", Cores: 2, MemoryMB: 4096,
		Env: map[string]string{"GHRM_JITCONFIG": "eyJhIjoiYiJ9==", "GHRM_INGEST_URL": "https://10.0.0.2:8443"},
	}
}

func TestSpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	cases := map[string]func(*EnvironmentSpec){
		"empty id":          func(s *EnvironmentSpec) { s.ID = "" },
		"uppercase id":      func(s *EnvironmentSpec) { s.ID = "01K6ABC" },
		"bad hostname":      func(s *EnvironmentSpec) { s.Hostname = "Bad_Host" },
		"hostname too long": func(s *EnvironmentSpec) { s.Hostname = string(make([]byte, 64)) },
		"zero cores":        func(s *EnvironmentSpec) { s.Cores = 0 },
		"tiny memory":       func(s *EnvironmentSpec) { s.MemoryMB = 128 },
		"lowercase env key": func(s *EnvironmentSpec) { s.Env["lower"] = "x" },
		"empty env key":     func(s *EnvironmentSpec) { s.Env[""] = "x" },
		"newline in value":  func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\nb" },
		"nul in value":      func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\x00b" },
		"carriage in value": func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\rb" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSpec()
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Validate() = %v, want ErrInvalidSpec", err)
			}
		})
	}
}

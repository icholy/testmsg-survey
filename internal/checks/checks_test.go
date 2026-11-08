package checks

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// analyze parses a test file body (without the package clause) and returns
// its checks.
func analyze(t *testing.T, src string) []Check {
	t.Helper()
	src = "package p\n\n" + src
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p_test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return AnalyzeFile(fset, f, []byte(src), "p_test.go", "example.com/p", nil).Checks
}

// summary renders checks as "mechanism sink bare|contextual producer@pkg".
func summary(checks []Check) string {
	var lines []string
	for _, c := range checks {
		kind := "contextual"
		if c.Bare {
			kind = "bare"
		}
		line := string(c.Mechanism) + " " + c.Sink + " " + kind
		if c.Producer != "" {
			line += " " + c.Producer + "@" + c.ProducerPkg
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func TestDirect(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "bare fatal",
			src: `
import ("os"; "testing")
func TestX(t *testing.T) {
	f, err := os.Open("x")
	if err != nil {
		t.Fatal(err)
	}
	_ = f
}`,
			want: "direct testing.Fatal bare os.Open@os",
		},
		{
			name: "bare format prefixes",
			src: `
import "testing"
func TestX(t *testing.T) {
	if err := run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := run(); err != nil {
		t.Errorf("got err %s, want nil", err.Error())
	}
	if err := run(); err != nil {
		t.Fatal("Failed:", err)
	}
}`,
			want: "direct testing.Fatalf bare run@.\n" +
				"direct testing.Errorf bare run@.\n" +
				"direct testing.Fatal bare run@.",
		},
		{
			name: "contextual",
			src: `
import ("os"; "testing")
func TestX(t *testing.T) {
	if err := os.WriteFile(name, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", name, err)
	}
	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := run(); err != nil {
		t.Fatal(name, err)
	}
}`,
			want: "direct testing.Fatalf contextual os.WriteFile@os\n" +
				"direct testing.Fatalf contextual run@.\n" +
				"direct testing.Fatal contextual run@.",
		},
		{
			name: "log then fail",
			src: `
import "testing"
func TestX(t *testing.T) {
	if err := run(); err != nil {
		t.Logf("running %s", name)
		t.FailNow()
	}
	if err := run(); err != nil {
		t.Log(err)
		t.FailNow()
	}
}`,
			want: "direct testing.FailNow contextual run@.\n" +
				"direct testing.FailNow bare run@.",
		},
		{
			name: "benchmark, TB and closures",
			src: `
import "testing"
func BenchmarkX(b *testing.B) {
	if err := run(); err != nil {
		b.Fatal(err)
	}
}
func helper(tb testing.TB) {
	if err := run(); err != nil {
		tb.Skip(err)
	}
}
func TestX(t *testing.T) {
	t.Run("sub", func(t *testing.T) {
		if err := run(); err != nil {
			t.Error(err)
		}
	})
	go func() {
		if err := run(); err != nil {
			t.Error(err)
		}
	}()
}`,
			want: "direct testing.Fatal bare run@.\n" +
				"direct testing.Skip bare run@.\n" +
				"direct testing.Error bare run@.\n" +
				"direct testing.Error bare run@.",
		},
		{
			name: "aliased testing import and suite T()",
			src: `
import tst "testing"
func TestX(t *tst.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
func (s *Suite) TestY() {
	if err := run(); err != nil {
		s.T().Fatal(err)
	}
}`,
			want: "direct testing.Fatal bare run@.\n" +
				"direct testing.Fatal bare run@.",
		},
		{
			name: "error names",
			src: `
import "testing"
func TestX(t *testing.T) {
	if errClose := run(); errClose != nil {
		t.Fatal(errClose)
	}
	if closeErr := run(); nil != closeErr {
		t.Fatal(closeErr)
	}
	if got := run(); got != nil {
		t.Fatal(got)
	}
	if error2 := run(); error2 != nil {
		t.Fatal(error2)
	}
}`,
			want: "direct testing.Fatal bare run@.\n" +
				"direct testing.Fatal bare run@.",
		},
		{
			name: "not sinks",
			src: `
import ("os"; "testing")
func TestX(t *testing.T) {
	if err := run(); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("expected error")
	}
	if err := run(); err != nil {
		if os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := run(); err != nil {
		x.Fatal(err)
	}
}`,
			want: "",
		},
		{
			name: "panic and log",
			src: `
import ("log"; "testing")
func TestX(t *testing.T) {
	if err := run(); err != nil {
		panic(err)
	}
	if err := run(); err != nil {
		log.Fatalf("run %s: %v", name, err)
	}
}`,
			want: "panic panic bare run@.\n" +
				"panic log.Fatalf contextual run@.",
		},
		{
			name: "wrapped",
			src: `
import ("fmt"; "testing")
func TestX(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(fmt.Errorf("unexpected: %w", err))
	}
	if err := run(); err != nil {
		t.Fatal(fmt.Errorf("decode %s: %w", name, err))
	}
}`,
			want: "direct testing.Fatal bare run@.\n" +
				"direct testing.Fatal contextual run@.",
		},
		{
			name: "producer from earlier assignment",
			src: `
import ("encoding/json"; "testing")
func TestX(t *testing.T) {
	var v any
	err := json.Unmarshal(data, &v)
	t.Log("decoded")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Do()
	if err != nil {
		t.Fatal(err)
	}
	x, err := parse[int](s)
	if err != nil {
		t.Fatal(err, x)
	}
	switch {
	case true:
		var err error = run()
		if err != nil {
			t.Fatal(err)
		}
	}
}`,
			want: "direct testing.Fatal bare json.Unmarshal@encoding/json\n" +
				"direct testing.Fatal bare c.Do@\n" +
				"direct testing.Fatal contextual parse[int]@\n" +
				"direct testing.Fatal bare run@.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summary(analyze(t, tt.src))
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestBareText(t *testing.T) {
	tests := map[string]bool{
		"%v":                          true,
		"%+v":                         true,
		"error: %v":                   true,
		"Unexpected error: %#v":       true,
		"expected no error, got %v":   true,
		"%s: %v":                      true,
		"open %s: %v":                 false,
		"Parse(%q) returned error %v": false,
		"setup failed: %v":            false,
		"failed to create temp dir":   false,
		"100%% failure":               false,
	}
	for s, want := range tests {
		if got := bareText(s); got != want {
			t.Errorf("bareText(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestDefaultImportName(t *testing.T) {
	tests := map[string]string{
		"testing":                             "testing",
		"github.com/stretchr/testify/require": "require",
		"gotest.tools/v3/assert":              "assert",
		"gopkg.in/check.v1":                   "check",
		"github.com/frankban/quicktest":       "quicktest",
		"github.com/maxatome/go-testdeep/td":  "td",
	}
	for path, want := range tests {
		if got := defaultImportName(path); got != want {
			t.Errorf("defaultImportName(%q) = %q, want %q", path, got, want)
		}
	}
}

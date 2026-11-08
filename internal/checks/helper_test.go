package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFiles writes files to a temporary directory and loads it.
func loadFiles(t *testing.T, fallback string, files map[string]string) *Module {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Load(root, fallback)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.ParseErrors != 0 {
		t.Fatalf("%d files failed to parse", m.ParseErrors)
	}
	return m
}

func TestHelpers(t *testing.T) {
	tests := []struct {
		name     string
		fallback string
		files    map[string]string
		want     string
	}{
		{
			name: "local helper",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import ("os"; "testing")
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestX(t *testing.T) {
	check(t, os.Remove("x"))
	err := os.Remove("y")
	check(t, err)
}`,
			},
			want: "helper helper:example.com/m.check bare os.Remove@os\n" +
				"helper helper:example.com/m.check bare os.Remove@os",
		},
		{
			name: "message parameters",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import (
	"testing"
	"github.com/stretchr/testify/require"
)
func ok(t testing.TB, err error, msg string) {
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}
func noErr(t *testing.T, err error, args ...any) {
	require.NoError(t, err, args...)
}
func TestX(t *testing.T) {
	err := run()
	ok(t, err, "open config")
	ok(t, err, "error")
	noErr(t, err)
	noErr(t, err, "item %d", i)
}`,
			},
			want: "helper helper:example.com/m.ok contextual run@.\n" +
				"helper helper:example.com/m.ok bare run@.\n" +
				"helper helper:example.com/m.noErr bare run@.\n" +
				"helper helper:example.com/m.noErr contextual run@.",
		},
		{
			name: "chained and generic helpers",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import ("strconv"; "testing")
func must(err error) { check2(err) }
func check2(e error) {
	if e != nil {
		panic(e)
	}
}
func must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
func TestX(t *testing.T) {
	err := run()
	must(err)
	n := must1(strconv.Atoi("1"))
	_ = n
}`,
			},
			want: "helper helper:example.com/m.must bare run@.\n" +
				"helper helper:example.com/m.must1 bare strconv.Atoi@strconv",
		},
		{
			name: "cross-package helper with a fixed message",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"internal/testutil/testutil.go": `package testutil
import "testing"
func Check(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
}`,
				"pkg/a/a_test.go": `package a_test
import (
	"os"
	"testing"
	"example.com/m/internal/testutil"
)
func TestX(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	testutil.Check(t, err)
	_ = dir
}`,
			},
			want: "helper helper:example.com/m/internal/testutil.Check bare os.MkdirTemp@os",
		},
		{
			name:     "GOPATH fallback",
			fallback: "github.com/x/y",
			files: map[string]string{
				"testutil/testutil.go": `package testutil
import "testing"
func OK(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}`,
				"a/a_test.go": `package a
import (
	"testing"
	"github.com/x/y/testutil"
)
func TestX(t *testing.T) {
	testutil.OK(t, run())
}`,
			},
			want: "helper helper:github.com/x/y/testutil.OK bare run@.",
		},
		{
			name: "method helpers on a fixture",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import "testing"
type env struct {
	t    *testing.T
	name string
}
func (e *env) must(err error) {
	if err != nil {
		e.t.Fatal(err)
	}
}
func (e *env) check(err error) {
	if err != nil {
		e.t.Fatalf("%s: %v", e.name, err)
	}
}
type fx struct {
	*testing.T
}
func (f fx) ok(err error) {
	if err != nil {
		f.Fatal(err)
	}
}
func TestX(t *testing.T) {
	e := &env{t: t}
	e.must(run())
	e.check(run())
	f := fx{t}
	f.ok(run())
}`,
			},
			want: "helper helper:example.com/m.(env).must bare run@.\n" +
				"helper helper:example.com/m.(env).check contextual run@.\n" +
				"helper helper:example.com/m.(fx).ok bare run@.",
		},
		{
			name: "external test package uses internal helper",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"export_test.go": `package p
import "testing"
func Must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}`,
				"p_ext_test.go": `package p_test
import (
	"testing"
	"example.com/m"
)
func TestX(t *testing.T) {
	p.Must(t, run())
}`,
			},
			want: "helper helper:example.com/m.Must bare run@.",
		},
		{
			name: "dot-imported helper package",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"testing/testing.go": `package testing
import "testing"
func Ok(tb testing.TB, err error) {
	if err != nil {
		tb.Fatal(err)
	}
}`,
				"cmd/cmd_test.go": `package cmd
import (
	"testing"
	. "example.com/m/testing"
)
func TestX(t *testing.T) {
	Ok(t, run())
}`,
			},
			want: "helper helper:example.com/m/testing.Ok bare run@.",
		},
		{
			name: "suite methods in a file without the import",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"suite_test.go": `package p
import "github.com/stretchr/testify/suite"
type testSuite struct{ suite.Suite }`,
				"x_test.go": `package p
func (s *testSuite) TestX() {
	err := run()
	s.Require().NoError(err)
	s.NoError(err)
}`,
			},
			want: "library testify.NoError bare run@.\n" +
				"library testify.NoError bare run@.",
		},
		{
			name: "closure helpers",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import ("os"; "testing")
func TestX(t *testing.T) {
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var mustf = func(err error, what string) {
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	check(os.Remove("x"))
	mustf(os.Remove("y"), "remove y")
	mustf(os.Remove("y"), "error")
	notHelper := func(err error) { t.Log(err) }
	notHelper(os.Remove("z"))
}`,
			},
			want: "helper helper:example.com/m.TestX.check bare os.Remove@os\n" +
				"helper helper:example.com/m.TestX.mustf contextual os.Remove@os\n" +
				"helper helper:example.com/m.TestX.mustf bare os.Remove@os",
		},
		{
			name: "not helpers",
			files: map[string]string{
				"go.mod": "module example.com/m\n",
				"p_test.go": `package p
import ("errors"; "testing")
func wrap(t *testing.T, err error) {
	if err := errors.Unwrap(err); err != nil {
		t.Fatal(err)
	}
}
func logIt(t *testing.T, err error) {
	t.Log(err)
}
func TestX(t *testing.T) {
	wrap(t, run())
	logIt(t, run())
}`,
			},
			want: "direct testing.Fatal bare errors.Unwrap@errors",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := loadFiles(t, tt.fallback, tt.files)
			got := summary(m.Analyze().Checks)
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestStdImportPaths(t *testing.T) {
	m := loadFiles(t, "", map[string]string{
		"src/go.mod": "module std\n",
		"src/internal/testenv/testenv.go": `package testenv
import "testing"
func Must(t testing.TB, err error) {
	if err != nil {
		t.Fatal(err)
	}
}`,
		"src/os/os_test.go": `package os_test
import (
	"testing"
	"internal/testenv"
)
func TestX(t *testing.T) {
	testenv.Must(t, run())
}`,
	})
	if got, want := summary(m.Analyze().Checks), "helper helper:internal/testenv.Must bare run@."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFuncKind(t *testing.T) {
	m := loadFiles(t, "", map[string]string{
		"go.mod": "module example.com/m\n",
		"p_test.go": `package p
import ("log"; "testing")
func TestX(t *testing.T) { if err := run(); err != nil { t.Fatal(err) } }
func Testable(t *testing.T) { if err := run(); err != nil { t.Fatal(err) } }
func BenchmarkX(b *testing.B) { if err := run(); err != nil { b.Fatal(err) } }
func FuzzX(f *testing.F) { if err := run(); err != nil { f.Fatal(err) } }
func Example() { if err := run(); err != nil { log.Fatal(err) } }
func ExampleFoo_bar() { if err := run(); err != nil { log.Fatal(err) } }
func TestMain(m *testing.M) { if err := run(); err != nil { log.Fatal(err) } }
`,
	})
	var got []string
	for _, c := range m.Analyze().Checks {
		got = append(got, c.Func)
	}
	want := "test other benchmark fuzz example example other"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestUnresolved(t *testing.T) {
	m := loadFiles(t, "", map[string]string{
		"go.mod": "module example.com/m\n",
		"p_test.go": `package p
import (
	"fmt"
	"testing"
	"github.com/other/testhelp"
	"github.com/stretchr/testify/require"
)
func TestX(t *testing.T) {
	err := run()
	testhelp.Check(t, err)
	fmt.Println(err)
	t.Log(err)
	require.Error(t, err)
	s.ErrorIs(err, target)
	if testhelp.Bad(err) {
	}
}`,
	})
	res := m.Analyze()
	var got []string
	for _, u := range res.Unresolved {
		got = append(got, u.Call)
	}
	if want := "testhelp.Check(t, err)"; strings.Join(got, "\n") != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

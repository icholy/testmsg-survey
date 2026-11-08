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

func TestLibrary(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "testify functions",
			src: `
import (
	"os"
	"testing"
	"github.com/stretchr/testify/assert"
	req "github.com/stretchr/testify/require"
)
func TestX(t *testing.T) {
	f, err := os.Open("x")
	req.NoError(t, err)
	req.NoError(t, os.Remove("x"))
	assert.NoError(t, err, "unexpected error")
	assert.NoErrorf(t, err, "open %s", name)
	assert.Nil(t, err)
	assert.Nil(t, f)
	assert.Error(t, err)
	if !assert.NoError(t, err) {
		return
	}
}`,
			want: "library github.com/stretchr/testify/require.NoError bare os.Open@os\n" +
				"library github.com/stretchr/testify/require.NoError bare os.Remove@os\n" +
				"library github.com/stretchr/testify/assert.NoError bare os.Open@os\n" +
				"library github.com/stretchr/testify/assert.NoErrorf contextual os.Open@os\n" +
				"library github.com/stretchr/testify/assert.Nil bare os.Open@os\n" +
				"library github.com/stretchr/testify/assert.NoError bare os.Open@os",
		},
		{
			name: "testify methods",
			src: `
import (
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)
func TestX(t *testing.T) {
	a := assert.New(t)
	a.NoError(run())
	err := run()
	a.NoError(err)
}
func (s *S) TestY() {
	err := run()
	s.NoError(err)
	s.Require().NoError(err, "running %s", name)
}`,
			want: "library testify.NoError bare run@.\n" +
				"library testify.NoError bare run@.\n" +
				"library testify.NoError bare run@.\n" +
				"library testify.NoError contextual run@.",
		},
		{
			name: "gotest.tools",
			src: `
import (
	"testing"
	"gotest.tools/v3/assert"
)
func TestX(t *testing.T) {
	err := run()
	assert.NilError(t, err)
	assert.Assert(t, err)
	assert.Check(t, err, "running %s", name)
	assert.Assert(t, ok)
}`,
			want: "library gotest.tools/v3/assert.NilError bare run@.\n" +
				"library gotest.tools/v3/assert.Assert bare run@.\n" +
				"library gotest.tools/v3/assert.Check contextual run@.",
		},
		{
			name: "quicktest",
			src: `
import (
	"testing"
	qt "github.com/frankban/quicktest"
)
func TestX(t *testing.T) {
	c := qt.New(t)
	err := run()
	c.Assert(err, qt.IsNil)
	c.Assert(err, qt.IsNil, qt.Commentf("running %s", name))
	qt.Assert(t, err, qt.IsNil)
	c.Assert(err, qt.Not(qt.IsNil))
}`,
			want: "library github.com/frankban/quicktest.C.Assert bare run@.\n" +
				"library github.com/frankban/quicktest.C.Assert contextual run@.\n" +
				"library github.com/frankban/quicktest.Assert bare run@.",
		},
		{
			name: "go-quicktest and check.v1",
			src: `
import (
	"testing"
	"github.com/go-quicktest/qt"
	. "gopkg.in/check.v1"
)
func TestX(t *testing.T) {
	err := run()
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsNil(err), qt.Commentf("%v", name))
}
func (s *S) TestY(c *C) {
	err := run()
	c.Assert(err, IsNil)
	c.Check(err, IsNil, Commentf("error: %v", err))
}`,
			want: "library github.com/go-quicktest/qt.Assert bare run@.\n" +
				"library github.com/go-quicktest/qt.Check contextual run@.\n" +
				"library gopkg.in/check.v1.C.Assert bare run@.\n" +
				"library gopkg.in/check.v1.C.Check bare run@.",
		},
		{
			name: "gomega",
			src: `
import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)
var _ = It("works", func() {
	err := run()
	Expect(err).NotTo(HaveOccurred())
	Expect(err).ToNot(HaveOccurred(), "running %s", name)
	Ω(err).ShouldNot(HaveOccurred())
	Expect(run()).To(Succeed())
	Expect(err).To(BeNil())
	Expect(err).To(HaveOccurred())
	g.Expect(err).NotTo(HaveOccurred())
})`,
			want: "library github.com/onsi/gomega.NotTo(HaveOccurred) bare run@.\n" +
				"library github.com/onsi/gomega.ToNot(HaveOccurred) contextual run@.\n" +
				"library github.com/onsi/gomega.ShouldNot(HaveOccurred) bare run@.\n" +
				"library github.com/onsi/gomega.To(Succeed) bare run@.\n" +
				"library github.com/onsi/gomega.To(BeNil) bare run@.\n" +
				"library github.com/onsi/gomega.NotTo(HaveOccurred) bare run@.",
		},
		{
			name: "is, goconvey, testdeep, be, shoenig, alecthomas",
			src: `
import (
	"testing"
	"github.com/matryer/is"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/maxatome/go-testdeep/td"
	"github.com/carlmjohnson/be"
	"github.com/shoenig/test/must"
	"github.com/alecthomas/assert/v2"
)
func TestX(t *testing.T) {
	is := is.New(t)
	err := run()
	is.NoErr(err)
	is.NoErr(err) // open config
	So(err, ShouldBeNil)
	td.CmpNoError(t, err)
	td.CmpNoError(t, err, "running %s", name)
	be.NilErr(t, err)
	must.NoError(t, err)
	assert.NoError(t, err)
}`,
			want: "library github.com/matryer/is.NoErr bare run@.\n" +
				"library github.com/matryer/is.NoErr contextual run@.\n" +
				"library github.com/smartystreets/goconvey/convey.So bare run@.\n" +
				"library github.com/maxatome/go-testdeep/td.CmpNoError bare run@.\n" +
				"library github.com/maxatome/go-testdeep/td.CmpNoError contextual run@.\n" +
				"library github.com/carlmjohnson/be.NilErr bare run@.\n" +
				"library github.com/shoenig/test/must.NoError bare run@.\n" +
				"library github.com/alecthomas/assert/v2.NoError bare run@.",
		},
		{
			name: "no library imported",
			src: `
import "testing"
func TestX(t *testing.T) {
	err := run()
	s.NoError(err)
	c.Assert(err, IsNil)
}`,
			want: "",
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

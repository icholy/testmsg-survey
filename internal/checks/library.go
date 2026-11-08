package checks

import (
	"go/ast"
	"strings"
)

// Library sinks, as listed in the README.
//
// A library sink is matched either as a package function (require.NoError)
// using the file's imports, or as a method on a receiver of unknown type
// (s.NoError, c.Assert) in a file that imports the library. In both cases the
// asserted argument must be an error value.

const (
	testifyAssert  = "github.com/stretchr/testify/assert"
	testifyRequire = "github.com/stretchr/testify/require"
	testifySuite   = "github.com/stretchr/testify/suite"
	gotestTools    = "gotest.tools/assert"
	gotestToolsV3  = "gotest.tools/v3/assert"
	quicktestOld   = "github.com/frankban/quicktest"
	quicktestNew   = "github.com/go-quicktest/qt"
	checkV1        = "gopkg.in/check.v1"
	gomega         = "github.com/onsi/gomega"
	matryerIs      = "github.com/matryer/is"
	goconvey       = "github.com/smartystreets/goconvey/convey"
	alecthomas     = "github.com/alecthomas/assert/v2"
	shoenigTest    = "github.com/shoenig/test"
	shoenigMust    = "github.com/shoenig/test/must"
	testdeep       = "github.com/maxatome/go-testdeep/td"
	be             = "github.com/carlmjohnson/be"
)

// libraries is every assertion library path. Calls into these libraries are
// resolved, whether or not they are sinks.
var libraries = []string{
	testifyAssert, testifyRequire, testifySuite, gotestTools, gotestToolsV3,
	quicktestOld, quicktestNew, checkV1, gomega, matryerIs, goconvey,
	alecthomas, shoenigTest, shoenigMust, testdeep, be,
}

// isLibrary reports whether path is an assertion library.
func isLibrary(path string) bool {
	for _, l := range libraries {
		if path == l {
			return true
		}
	}
	return false
}

// libraryMethods are assertion method names that are not sinks (they check
// that an error occurred, or compare values). In a file that imports an
// assertion library, method calls with these names are treated as resolved.
var libraryMethods = map[string]bool{
	"Error": true, "Errorf": true, "ErrorIs": true, "ErrorIsf": true,
	"ErrorAs": true, "ErrorAsf": true, "ErrorContains": true,
	"ErrorContainsf": true, "EqualError": true, "EqualErrorf": true,
	"NotErrorIs": true, "NotErrorIsf": true, "Equal": true, "Equalf": true,
	"NotEqual": true, "NotEqualf": true, "EqualValues": true, "Same": true,
	"True": true, "Truef": true, "False": true, "Falsef": true,
	"NotNil": true, "NotNilf": true, "Nil": true, "Nilf": true,
	"Contains": true, "NotContains": true, "Empty": true, "NotEmpty": true,
	"IsType": true, "Implements": true, "Panics": true, "Assert": true,
	"Check": true, "Expect": true, "Equals": true, "So": true,
	"Cmp": true, "CmpError": true, "CmpErrorIs": true,
}

// typedSinks are library functions whose asserted parameter has type error,
// so any expression passed to them counts as the asserted error. Other sinks
// (Nil, Assert, IsNil, BeNil, ShouldBeNil) accept any value, and the
// argument must be an error value by name.
var typedSinks = map[string]bool{
	"NoError": true, "NoErrorf": true, "NilError": true, "NoErr": true,
	"NilErr": true, "CmpNoError": true, "RequireNoError": true,
}

// asserted returns the asserted error expression for a sink argument. If
// typed is true any expression counts; otherwise it must be an error value.
func asserted(e ast.Expr, typed bool) (ast.Expr, bool) {
	if typed {
		return ast.Unparen(e), true
	}
	id, ok := errorValue(e)
	if !ok {
		return nil, false
	}
	return id, true
}

// errName returns the name of an asserted error if it is an identifier.
func errName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// librarySink returns the sink name, the asserted error and whether the
// message is bare if call is a library sink.
func (fc *fileCtx) librarySink(call *ast.CallExpr) (string, ast.Expr, bool, bool) {
	if id, ok := call.Fun.(*ast.Ident); ok {
		// An unqualified call to a dot-imported library function, such as
		// So(err, ShouldBeNil).
		for _, path := range fc.dotImports {
			if s, e, bare, ok := fc.librarySinkNamed(call, id.Name, path, call.Args); ok {
				return s, e, bare, ok
			}
		}
		return "", nil, false, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false, false
	}
	name := sel.Sel.Name
	pkg := fc.pkgOf(sel.X)
	args := call.Args
	return fc.librarySinkNamed(call, name, pkg, args)
}

// librarySinkNamed implements librarySink for a call to name, qualified by
// the import path pkg ("" if unqualified or a method).
func (fc *fileCtx) librarySinkNamed(call *ast.CallExpr, name, pkg string, args []ast.Expr) (string, ast.Expr, bool, bool) {

	// msgAndArgs style: the error is at position i and everything after it
	// is the message.
	msgAt := func(i int) (ast.Expr, bool, bool) {
		if len(args) <= i {
			return nil, false, false
		}
		e, ok := asserted(args[i], typedSinks[name])
		if !ok {
			return nil, false, false
		}
		return e, fc.bareArgs(Message{Args: args[i+1:], Format: true}, errName(e)), true
	}
	sink := func(path string) string { return path + "." + name }

	switch pkg {
	case testifyAssert, testifyRequire:
		switch name {
		case "NoError", "NoErrorf", "Nil", "Nilf":
			if id, bare, ok := msgAt(1); ok {
				return sink(pkg), id, bare, true
			}
		}
		return "", nil, false, false
	case gotestTools, gotestToolsV3:
		switch name {
		case "NilError", "Assert", "Check":
			if id, bare, ok := msgAt(1); ok {
				return sink(pkg), id, bare, true
			}
		}
		return "", nil, false, false
	case alecthomas:
		if name == "NoError" {
			if id, bare, ok := msgAt(1); ok {
				return sink(pkg), id, bare, true
			}
		}
		return "", nil, false, false
	case shoenigTest, shoenigMust:
		switch name {
		case "NoError", "Nil":
			if id, bare, ok := msgAt(1); ok {
				return sink(pkg), id, bare, true
			}
		}
		return "", nil, false, false
	case testdeep:
		switch name {
		case "CmpNoError", "NoError", "RequireNoError":
			if id, bare, ok := msgAt(1); ok {
				return sink(pkg), id, bare, true
			}
		}
		return "", nil, false, false
	case be:
		if name == "NilErr" && len(args) == 2 {
			if id, ok := asserted(args[1], true); ok {
				return sink(pkg), id, true, true
			}
		}
		return "", nil, false, false
	case quicktestOld:
		// qt.Assert(t, err, qt.IsNil, comments...)
		if (name == "Assert" || name == "Check") && len(args) >= 3 && fc.isLibIdent(args[2], quicktestOld, "IsNil") {
			if id, ok := errorValue(args[1]); ok {
				return sink(pkg), id, fc.bareComments(args[3:], errName(id)), true
			}
		}
		return "", nil, false, false
	case quicktestNew:
		// qt.Assert(t, qt.IsNil(err), comments...)
		if (name == "Assert" || name == "Check") && len(args) >= 2 {
			if id, ok := fc.libCallArg(args[1], quicktestNew, "IsNil"); ok {
				return sink(pkg), id, fc.bareComments(args[2:], errName(id)), true
			}
		}
		return "", nil, false, false
	case goconvey:
		// So(err, ShouldBeNil)
		if name == "So" && len(args) == 2 && fc.isLibIdent(args[1], goconvey, "ShouldBeNil") {
			if id, ok := errorValue(args[0]); ok {
				return sink(pkg), id, true, true
			}
		}
		return "", nil, false, false
	case "":
		// A method on a receiver of unknown type, or an unqualified call.
	default:
		return "", nil, false, false
	}

	// Methods on receivers of unknown type, only in files that import the
	// library.
	switch {
	case fc.importsAny(testifyAssert, testifyRequire, testifySuite):
		switch name {
		case "NoError", "NoErrorf", "Nil", "Nilf":
			// a.NoError(err, msgAndArgs...) where a is assert.New(t),
			// a suite or s.Require().
			if id, bare, ok := msgAt(0); ok {
				return "testify." + name, id, bare, true
			}
		}
	}
	if fc.importsAny(quicktestOld) && (name == "Assert" || name == "Check") && len(args) >= 2 && fc.isLibIdent(args[1], quicktestOld, "IsNil") {
		// c.Assert(err, qt.IsNil, comments...) where c is *qt.C.
		if id, ok := errorValue(args[0]); ok {
			return quicktestOld + ".C." + name, id, fc.bareComments(args[2:], errName(id)), true
		}
	}
	if fc.importsAny(checkV1) && (name == "Assert" || name == "Check") && len(args) >= 2 && fc.isLibIdent(args[1], checkV1, "IsNil") {
		// c.Assert(err, check.IsNil, comments...) where c is *check.C.
		if id, ok := errorValue(args[0]); ok {
			return checkV1 + ".C." + name, id, fc.bareComments(args[2:], errName(id)), true
		}
	}
	if fc.importsAny(matryerIs) && name == "NoErr" && len(args) == 1 {
		// is.NoErr(err) // comment
		if id, ok := asserted(args[0], true); ok {
			return matryerIs + ".NoErr", id, !fc.hasLineComment(call), true
		}
	}
	if fc.importsAny(gomega) {
		if s, id, bare, ok := fc.gomegaSink(call); ok {
			return s, id, bare, true
		}
	}
	return "", nil, false, false
}

// gomegaSink matches Expect(err).NotTo(HaveOccurred()) and its variants.
// Expect may be qualified, dot-imported or a method on a gomega.G.
func (fc *fileCtx) gomegaSink(call *ast.CallExpr) (string, ast.Expr, bool, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return "", nil, false, false
	}
	var want string
	switch sel.Sel.Name {
	case "NotTo", "ToNot", "ShouldNot":
		want = "HaveOccurred"
	case "To", "Should":
		want = "Succeed|BeNil"
	default:
		return "", nil, false, false
	}
	matcher, ok := ast.Unparen(call.Args[0]).(*ast.CallExpr)
	if !ok || len(matcher.Args) != 0 || !strings.Contains("|"+want+"|", "|"+calleeName(matcher)+"|") {
		return "", nil, false, false
	}
	expect, ok := ast.Unparen(sel.X).(*ast.CallExpr)
	if !ok || len(expect.Args) == 0 {
		return "", nil, false, false
	}
	switch calleeName(expect) {
	case "Expect", "Ω", "ExpectWithOffset", "OmegaWithOffset":
	default:
		return "", nil, false, false
	}
	errArg := expect.Args[len(expect.Args)-1]
	if strings.HasSuffix(calleeName(expect), "WithOffset") && len(expect.Args) < 2 {
		return "", nil, false, false
	}
	id, ok := asserted(errArg, calleeName(matcher) == "Succeed")
	if !ok {
		return "", nil, false, false
	}
	bare := fc.bareArgs(Message{Args: call.Args[1:], Format: true}, errName(id))
	return gomega + "." + sel.Sel.Name + "(" + calleeName(matcher) + ")", id, bare, true
}

// calleeName returns the final identifier of a call's function.
func calleeName(call *ast.CallExpr) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// importsAny reports whether the file, or any file in its package, imports
// any of the paths. Suite methods such as s.Require().NoError are often
// called from files that do not import the library themselves.
func (fc *fileCtx) importsAny(paths ...string) bool {
	for _, p := range paths {
		if fc.importsPath[p] || (fc.pkg != nil && fc.pkg.imports[p]) {
			return true
		}
	}
	return false
}

// isLibIdent reports whether e is path.name, or an unqualified name in a file
// that dot-imports or otherwise imports path.
func (fc *fileCtx) isLibIdent(e ast.Expr, path, name string) bool {
	switch e := ast.Unparen(e).(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == name && fc.pkgOf(e.X) == path
	case *ast.Ident:
		return e.Name == name && fc.importsPath[path]
	}
	return false
}

// libCallArg returns the error argument of path.name(err).
func (fc *fileCtx) libCallArg(e ast.Expr, path, name string) (ast.Expr, bool) {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !fc.isLibIdent(call.Fun, path, name) {
		return nil, false
	}
	return errorValue(call.Args[0])
}

// bareComments classifies quicktest and check.v1 comment arguments. Commentf
// calls are classified by their format arguments; any other comment value is
// contextual.
func (fc *fileCtx) bareComments(args []ast.Expr, errName string) bool {
	for _, a := range args {
		call, ok := ast.Unparen(a).(*ast.CallExpr)
		if !ok || calleeName(call) != "Commentf" {
			return false
		}
		if !fc.bareArgs(Message{Args: call.Args, Format: true}, errName) {
			return false
		}
	}
	return true
}

// hasLineComment reports whether a comment starts on the line where call
// ends, after the call.
func (fc *fileCtx) hasLineComment(call *ast.CallExpr) bool {
	end := fc.fset.Position(call.End())
	for _, cg := range fc.file.Comments {
		p := fc.fset.Position(cg.Pos())
		if p.Line == end.Line && p.Offset >= end.Offset {
			return true
		}
	}
	return false
}

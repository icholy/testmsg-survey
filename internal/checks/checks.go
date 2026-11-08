// Package checks finds and classifies error checks in Go test files.
//
// The rules implemented here are the ones in the Definitions section of the
// repository README.
package checks

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// Mechanism tags how an error check fails the test.
type Mechanism string

const (
	Direct  Mechanism = "direct"
	Library Mechanism = "library"
	Panic   Mechanism = "panic"
	Helper  Mechanism = "helper"
)

// Check is one error check site.
type Check struct {
	File      string    `json:"file"`
	Line      int       `json:"line"`
	Mechanism Mechanism `json:"mechanism"`
	// Sink names the failing call, such as "testing.Fatal",
	// "github.com/stretchr/testify/require.NoError" or "helper:mustOpen".
	Sink string `json:"sink"`
	Bare bool   `json:"bare"`
	// Producer is the source text of the function called to produce the
	// error, if one was found, and ProducerPkg is its import path: "." for
	// the current package and "" if it is unknown (for example a method).
	Producer    string `json:"producer,omitempty"`
	ProducerPkg string `json:"producer_pkg,omitempty"`
	// Code is the source text of the check, for manual review.
	Code string `json:"code"`
}

// Unresolved is a call that passes an error value to a function the analyser
// could not identify as a sink or a non-sink.
type Unresolved struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Call string `json:"call"`
}

// fileCtx holds per-file state.
type fileCtx struct {
	fset    *token.FileSet
	file    *ast.File
	src     []byte
	name    string
	imports map[string]string // local name -> import path
	pkgPath string            // import path of the file's package, if known
	helpers *Helpers
}

func newFileCtx(fset *token.FileSet, file *ast.File, src []byte, name, pkgPath string, helpers *Helpers) *fileCtx {
	fc := &fileCtx{
		fset:    fset,
		file:    file,
		src:     src,
		name:    name,
		imports: map[string]string{},
		pkgPath: pkgPath,
		helpers: helpers,
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		var local string
		switch {
		case imp.Name != nil:
			local = imp.Name.Name
		default:
			local = defaultImportName(path)
		}
		if local == "_" || local == "." {
			continue
		}
		fc.imports[local] = path
	}
	return fc
}

// defaultImportName guesses the package name for an import path: the last
// element, ignoring a major version suffix and a gopkg.in version suffix.
func defaultImportName(path string) string {
	elems := strings.Split(path, "/")
	name := elems[len(elems)-1]
	if len(elems) > 1 && len(name) >= 2 && name[0] == 'v' && isDigits(name[1:]) {
		name = elems[len(elems)-2]
	}
	if i := strings.Index(name, ".v"); i > 0 && isDigits(name[i+2:]) {
		name = name[:i]
	}
	name = strings.TrimPrefix(name, "go-")
	return strings.ReplaceAll(name, "-", "_")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// pkgOf returns the import path for a selector qualifier such as "os" in
// os.Open, or "" if x is not an imported package name.
func (fc *fileCtx) pkgOf(x ast.Expr) string {
	id, ok := x.(*ast.Ident)
	if !ok || id.Obj != nil {
		return ""
	}
	return fc.imports[id.Name]
}

// isPkgFunc reports whether call is a call to path.name.
func (fc *fileCtx) isPkgFunc(call *ast.CallExpr, path, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name && fc.pkgOf(sel.X) == path
}

func (fc *fileCtx) line(n ast.Node) int {
	return fc.fset.Position(n.Pos()).Line
}

// text returns the source text of n, with whitespace collapsed and long
// snippets truncated.
func (fc *fileCtx) text(n ast.Node) string {
	start := fc.fset.Position(n.Pos()).Offset
	end := fc.fset.Position(n.End()).Offset
	if start < 0 || end > len(fc.src) || start > end {
		return ""
	}
	s := strings.Join(strings.Fields(string(fc.src[start:end])), " ")
	const max = 400
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// isTestingType reports whether a parameter type is *testing.T, *testing.B,
// *testing.F or testing.TB.
func (fc *fileCtx) isTestingType(e ast.Expr) bool {
	ptr := false
	if star, ok := e.(*ast.StarExpr); ok {
		e, ptr = star.X, true
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || fc.pkgOf(sel.X) != "testing" {
		return false
	}
	switch sel.Sel.Name {
	case "T", "B", "F":
		return ptr
	case "TB":
		return !ptr
	}
	return false
}

// stack is the chain of enclosing nodes during traversal.
type stack []ast.Node

// funcParams returns the parameter lists of the enclosing functions, innermost
// first.
func (s stack) funcTypes() []*ast.FuncType {
	var types []*ast.FuncType
	for i := len(s) - 1; i >= 0; i-- {
		switch n := s[i].(type) {
		case *ast.FuncDecl:
			types = append(types, n.Type)
		case *ast.FuncLit:
			types = append(types, n.Type)
		}
	}
	return types
}

// isTestingValue reports whether e is a testing value in the current scope:
// an identifier declared as a testing type in an enclosing function, or a
// call X.T().
func (fc *fileCtx) isTestingValue(e ast.Expr, s stack) bool {
	e = ast.Unparen(e)
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 0 {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "T"
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	for _, ft := range s.funcTypes() {
		if ft.Params == nil {
			continue
		}
		for _, f := range ft.Params.List {
			for _, n := range f.Names {
				if n.Name == id.Name {
					return fc.isTestingType(f.Type)
				}
			}
		}
	}
	return false
}

// reportMethods are the testing methods that fail or skip a test.
var reportMethods = map[string]bool{
	"Fatal": true, "Fatalf": true, "Error": true, "Errorf": true,
	"Skip": true, "Skipf": true, "Fail": true, "FailNow": true,
}

// logMethods are testing methods whose output counts toward the message.
var logMethods = map[string]bool{"Log": true, "Logf": true}

// logFuncs are the log package functions that count as panic sinks.
var logFuncs = map[string]bool{
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"Panic": true, "Panicf": true, "Panicln": true,
}

// nilCheck returns the error value X if cond is X != nil or nil != X.
func nilCheck(cond ast.Expr) (*ast.Ident, bool) {
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return nil, false
	}
	x, y := bin.X, bin.Y
	if isNil(x) {
		x, y = y, x
	}
	if !isNil(y) {
		return nil, false
	}
	return errorValue(x)
}

func isNil(e ast.Expr) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && id.Name == "nil"
}

// testingCall returns the method name if call is a method call on a testing
// value.
func (fc *fileCtx) testingCall(call *ast.CallExpr, s stack) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !fc.isTestingValue(sel.X, s) {
		return "", false
	}
	return sel.Sel.Name, true
}

// ifSink inspects the body of if X != nil and returns the sink it contains,
// if any. It returns the mechanism, the sink name and whether the messages
// are bare. allowed is passed through to bareArgs.
func (fc *fileCtx) ifSink(body *ast.BlockStmt, errName string, s stack, allowed func(ast.Expr) bool) (Mechanism, string, bool, bool) {
	var (
		mech  Mechanism
		sink  string
		msgs  []Message
		found bool
	)
	for _, stmt := range body.List {
		es, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if name, ok := fc.testingCall(call, s); ok {
			switch {
			case reportMethods[name]:
				if !found || mech != Direct {
					mech, sink, found = Direct, "testing."+name, true
				}
				msgs = append(msgs, Message{Args: call.Args, Format: strings.HasSuffix(name, "f")})
			case logMethods[name]:
				msgs = append(msgs, Message{Args: call.Args, Format: name == "Logf"})
			}
			continue
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" && id.Obj == nil && len(call.Args) == 1 {
			if !found {
				mech, sink, found = Panic, "panic", true
			}
			msgs = append(msgs, Message{Args: call.Args})
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && logFuncs[sel.Sel.Name] && fc.pkgOf(sel.X) == "log" {
			if !found {
				mech, sink, found = Panic, "log."+sel.Sel.Name, true
			}
			msgs = append(msgs, Message{Args: call.Args, Format: strings.HasSuffix(sel.Sel.Name, "f")})
		}
	}
	if !found {
		return "", "", false, false
	}
	bare := true
	for _, m := range msgs {
		if !fc.bareArgs(m, errName, allowed) {
			bare = false
		}
	}
	return mech, sink, bare, true
}

// producer finds the call that produced the error named errName for the if
// statement at the top of the stack, from the if initialiser or the closest
// earlier assignment in the same statement list.
func (fc *fileCtx) producer(ifs *ast.IfStmt, errName string, s stack) (string, string) {
	if call := assignedCall(ifs.Init, errName); call != nil {
		return fc.describeCall(call)
	}
	var list []ast.Stmt
	if len(s) >= 2 {
		switch p := s[len(s)-2].(type) {
		case *ast.BlockStmt:
			list = p.List
		case *ast.CaseClause:
			list = p.Body
		case *ast.CommClause:
			list = p.Body
		}
	}
	idx := -1
	for i, stmt := range list {
		if stmt == ifs {
			idx = i
			break
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if call := assignedCall(list[i], errName); call != nil {
			return fc.describeCall(call)
		}
		if assigns(list[i], errName) {
			break
		}
	}
	return "", ""
}

// assignedCall returns the call on the right-hand side of stmt if stmt
// assigns errName from a single call.
func assignedCall(stmt ast.Stmt, errName string) *ast.CallExpr {
	var lhs, rhs []ast.Expr
	switch st := stmt.(type) {
	case *ast.AssignStmt:
		lhs, rhs = st.Lhs, st.Rhs
	case *ast.DeclStmt:
		gd, ok := st.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR || len(gd.Specs) != 1 {
			return nil
		}
		vs := gd.Specs[0].(*ast.ValueSpec)
		for _, n := range vs.Names {
			lhs = append(lhs, n)
		}
		rhs = vs.Values
	default:
		return nil
	}
	for i, l := range lhs {
		id, ok := l.(*ast.Ident)
		if !ok || id.Name != errName {
			continue
		}
		var r ast.Expr
		switch {
		case len(rhs) == 1:
			r = rhs[0]
		case i < len(rhs):
			r = rhs[i]
		default:
			return nil
		}
		call, _ := ast.Unparen(r).(*ast.CallExpr)
		return call
	}
	return nil
}

// assigns reports whether stmt assigns to errName in any way.
func assigns(stmt ast.Stmt, errName string) bool {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok {
		return false
	}
	for _, l := range as.Lhs {
		if id, ok := l.(*ast.Ident); ok && id.Name == errName {
			return true
		}
	}
	return false
}

// describeCall returns the source text of the called function and its
// package.
func (fc *fileCtx) describeCall(call *ast.CallExpr) (string, string) {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return fun.Name, "."
	case *ast.SelectorExpr:
		if pkg := fc.pkgOf(fun.X); pkg != "" {
			return fc.text(fun), pkg
		}
		return fc.text(fun), ""
	case *ast.IndexExpr, *ast.IndexListExpr:
		return fc.text(fun), ""
	}
	return fc.text(call.Fun), ""
}

// walk calls fn for every node in the file with the stack of enclosing nodes,
// the last element of which is the node itself.
func walk(file *ast.File, fn func(n ast.Node, s stack)) {
	var s stack
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			s = s[:len(s)-1]
			return true
		}
		s = append(s, n)
		fn(n, s)
		return true
	})
}

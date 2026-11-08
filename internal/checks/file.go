package checks

import (
	"go/ast"
	"go/token"
	"strings"
)

// Result is the result of analysing a module.
type Result struct {
	Checks     []Check
	Unresolved []Unresolved
	// TestFiles is the number of _test.go files analysed.
	TestFiles int
}

// Analyze resolves helpers across the module and then finds the error checks
// in every _test.go file.
func (m *Module) Analyze() Result {
	m.resolveHelpers()
	var res Result
	for _, f := range m.Files {
		if !f.Test {
			continue
		}
		res.TestFiles++
		m.fileCtx(f).analyze(&res)
	}
	return res
}

// AnalyzeFile finds the error checks in one parsed test file on its own,
// without helper resolution.
func AnalyzeFile(fset *token.FileSet, file *ast.File, src []byte, name string) Result {
	var res Result
	newFileCtx(fset, file, src, name, nil, nil).analyze(&res)
	return res
}

func (fc *fileCtx) analyze(res *Result) {
	walk(fc.file, func(n ast.Node, s stack) {
		switch n := n.(type) {
		case *ast.CallExpr:
			fc.analyzeCall(n, s, res)
		case *ast.IfStmt:
			fc.analyzeIf(n, s, res)
		}
	})
}

func (fc *fileCtx) analyzeIf(ifs *ast.IfStmt, s stack, res *Result) {
	errID, ok := nilCheck(ifs.Cond)
	if !ok {
		return
	}
	mech, sink, bare, ok := fc.ifSink(ifs.Body, errID.Name, s)
	if !ok || fc.insideHelper(s, errID.Name) {
		return
	}
	c := Check{
		File:      fc.name,
		Line:      fc.line(ifs),
		Mechanism: mech,
		Func:      funcKind(s),
		Sink:      sink,
		Bare:      bare,
		Code:      fc.text(ifs),
	}
	c.Producer, c.ProducerPkg = fc.producer(errID.Name, s)
	res.Checks = append(res.Checks, c)
}

func (fc *fileCtx) analyzeCall(call *ast.CallExpr, s stack, res *Result) {
	mech := Library
	sink, e, bare, ok := fc.librarySink(call)
	if !ok {
		mech = Helper
		sink, e, bare, ok = fc.helperCall(call)
	}
	if !ok {
		fc.unresolved(call, s, res)
		return
	}
	if fc.insideHelper(s, errName(e)) {
		return
	}
	c := Check{
		File:      fc.name,
		Line:      fc.line(call),
		Mechanism: mech,
		Func:      funcKind(s),
		Sink:      sink,
		Bare:      bare,
		Code:      fc.text(call),
	}
	if inner, ok := e.(*ast.CallExpr); ok {
		c.Producer, c.ProducerPkg = fc.describeCall(inner)
	} else if name := errName(e); name != "" {
		c.Producer, c.ProducerPkg = fc.producer(name, s)
	}
	res.Checks = append(res.Checks, c)
}

// insideHelper reports whether the node at the top of the stack is inside a
// helper whose forwarded error parameter is errName. Such sinks are counted
// at the helper's call sites instead.
func (fc *fileCtx) insideHelper(s stack, errName string) bool {
	if fc.mod == nil || errName == "" {
		return false
	}
	for i := len(s) - 1; i >= 0; i-- {
		switch n := s[i].(type) {
		case *ast.FuncLit:
			if fi := fc.mod.funcByNode(fc.pkg, n); fi != nil && fi.Helper != nil {
				return fi.Params[fi.Helper.ErrParam].Name == errName
			}
			for _, p := range flattenParams(n.Type) {
				if p.Name == errName {
					return false
				}
			}
		case *ast.FuncDecl:
			fi := fc.mod.funcByNode(fc.pkg, n)
			if fi == nil || fi.Helper == nil {
				return false
			}
			return fi.Params[fi.Helper.ErrParam].Name == errName
		}
	}
	return false
}

// unresolved records a statement call that passes an error value to a
// function that is not a known sink, outside the standard library and not on
// a testing value.
func (fc *fileCtx) unresolved(call *ast.CallExpr, s stack, res *Result) {
	if len(s) < 2 {
		return
	}
	if _, ok := s[len(s)-2].(*ast.ExprStmt); !ok {
		return
	}
	hasErr := false
	for _, a := range call.Args {
		if _, ok := errorValue(a); ok {
			hasErr = true
			break
		}
	}
	if !hasErr {
		return
	}
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		if fun.Obj == nil && builtins[fun.Name] {
			return
		}
	case *ast.SelectorExpr:
		if path := fc.pkgOf(fun.X); path != "" && isStdlib(path) {
			return
		}
		if fc.isTestingValue(fun.X, s) {
			return
		}
	}
	res.Unresolved = append(res.Unresolved, Unresolved{
		File: fc.name,
		Line: fc.line(call),
		Call: fc.text(call),
	})
}

var builtins = map[string]bool{
	"panic": true, "print": true, "println": true, "close": true,
	"delete": true, "append": true, "copy": true, "clear": true,
}

// isStdlib reports whether an import path belongs to the standard library:
// its first element has no dot.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

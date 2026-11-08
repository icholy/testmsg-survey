package checks

import (
	"go/ast"
	"go/token"
)

// Helpers is the set of helper sinks known for a module. It is filled in by
// the fixed-point pass over the module before test files are analysed.
type Helpers struct{}

// FileResult is the result of analysing one test file.
type FileResult struct {
	Checks     []Check
	Unresolved []Unresolved
}

// AnalyzeFile finds the error checks in one parsed test file. pkgPath is the
// import path of the file's package, or "" if unknown.
func AnalyzeFile(fset *token.FileSet, file *ast.File, src []byte, name, pkgPath string, helpers *Helpers) FileResult {
	fc := newFileCtx(fset, file, src, name, pkgPath, helpers)
	var res FileResult
	walk(file, func(n ast.Node, s stack) {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return
		}
		errID, ok := nilCheck(ifs.Cond)
		if !ok {
			return
		}
		mech, sink, bare, ok := fc.ifSink(ifs.Body, errID.Name, s, nil)
		if !ok {
			return
		}
		c := Check{
			File:      name,
			Line:      fc.line(ifs),
			Mechanism: mech,
			Sink:      sink,
			Bare:      bare,
			Code:      fc.text(ifs),
		}
		c.Producer, c.ProducerPkg = fc.producer(ifs, errID.Name, s)
		res.Checks = append(res.Checks, c)
	})
	return res
}

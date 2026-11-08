package checks

import (
	"go/ast"
)

// HelperInfo describes a function that forwards one of its error parameters,
// unchanged, to a sink.
type HelperInfo struct {
	// ErrParam is the index of the forwarded error parameter.
	ErrParam int
	// MsgParams are the indices of parameters that reach the message.
	MsgParams map[int]bool
	// Bare reports whether the sink inside the helper is bare when the
	// message parameters are bare. Fixed strings inside the helper count as
	// bare.
	Bare bool
	// Sink is the sink inside the helper, such as "testing.Fatal" or
	// "helper:check".
	Sink string
}

func (h *HelperInfo) equal(o *HelperInfo) bool {
	if h == nil || o == nil {
		return h == o
	}
	if h.ErrParam != o.ErrParam || h.Bare != o.Bare || h.Sink != o.Sink || len(h.MsgParams) != len(o.MsgParams) {
		return false
	}
	for k := range h.MsgParams {
		if !o.MsgParams[k] {
			return false
		}
	}
	return true
}

// maxHelperRounds bounds the fixed-point iteration.
const maxHelperRounds = 20

// resolveHelpers computes the helper set as a fixed point over every function
// in the module.
func (m *Module) resolveHelpers() {
	var funcs []*FuncInfo
	ctxs := map[*File]*fileCtx{}
	for _, f := range m.Files {
		ctxs[f] = m.fileCtx(f)
	}
	for _, f := range m.Files {
		for _, decl := range f.AST.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fi := m.funcByDecl(f.Pkg, fd); fi != nil {
				funcs = append(funcs, fi)
			}
		}
	}
	for round := 0; round < maxHelperRounds; round++ {
		changed := false
		for _, fi := range funcs {
			h := ctxs[fi.File].helperInfo(fi)
			if !h.equal(fi.Helper) {
				fi.Helper = h
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// funcByDecl returns the FuncInfo for a declaration.
func (m *Module) funcByDecl(p *Package, fd *ast.FuncDecl) *FuncInfo {
	if fd.Recv == nil {
		if fi := p.funcs[fd.Name.Name]; fi != nil && fi.Decl == fd {
			return fi
		}
		return nil
	}
	for _, fi := range p.methods[fd.Name.Name] {
		if fi.Decl == fd {
			return fi
		}
	}
	return nil
}

func (m *Module) fileCtx(f *File) *fileCtx {
	return newFileCtx(m.Fset, f.AST, f.Src, f.Name, m, f.Pkg)
}

// isErrorType reports whether a parameter type is error.
func isErrorType(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "error"
}

// helperInfo analyses a function body and returns its HelperInfo, or nil if
// it does not forward an error parameter to a sink.
func (fc *fileCtx) helperInfo(fi *FuncInfo) *HelperInfo {
	errParams := map[string]int{}
	for i, p := range fi.Params {
		if isErrorType(p.Type) && !p.Variadic && p.Name != "_" && !assignedIn(fi.Decl.Body, p.Name) {
			errParams[p.Name] = i
		}
	}
	if len(errParams) == 0 {
		return nil
	}
	var info *HelperInfo
	msgParams := map[int]bool{}
	fc.helperMode = true
	fc.allowed = func(e ast.Expr) bool {
		id, ok := ast.Unparen(e).(*ast.Ident)
		if !ok {
			return false
		}
		for i, p := range fi.Params {
			if p.Name == id.Name {
				if _, isErr := errParams[p.Name]; isErr {
					return false
				}
				msgParams[i] = true
				return true
			}
		}
		return false
	}
	defer func() {
		fc.helperMode = false
		fc.allowed = nil
	}()
	walk(fi.Decl, func(n ast.Node, s stack) {
		if info != nil {
			return
		}
		// Only the message parameters of the sink that is found count.
		clear(msgParams)
		switch n := n.(type) {
		case *ast.IfStmt:
			id, ok := nilCheckAny(n.Cond)
			if !ok {
				return
			}
			idx, ok := errParams[id.Name]
			if !ok || shadowed(s, fi.Decl, id.Name) {
				return
			}
			_, sink, bare, ok := fc.ifSink(n.Body, id.Name, s)
			if ok {
				info = &HelperInfo{ErrParam: idx, Bare: bare, Sink: sink}
			}
		case *ast.CallExpr:
			sink, e, bare, ok := fc.sinkCall(n)
			if !ok {
				return
			}
			id, ok := e.(*ast.Ident)
			if !ok {
				return
			}
			idx, ok := errParams[id.Name]
			if !ok || shadowed(s, fi.Decl, id.Name) {
				return
			}
			info = &HelperInfo{ErrParam: idx, Bare: bare, Sink: sink}
		}
	})
	if info != nil {
		info.MsgParams = msgParams
	}
	return info
}

// assignedIn reports whether name is assigned or redeclared anywhere in body,
// in which case a parameter of that name is not forwarded unchanged.
func assignedIn(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				if id.Name == name {
					found = true
				}
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{n.Key, n.Value} {
				if id, ok := e.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// shadowed reports whether name is redeclared as a parameter of a function
// literal between the declaration and the current node.
func shadowed(s stack, decl *ast.FuncDecl, name string) bool {
	for i := len(s) - 1; i >= 0 && s[i] != decl; i-- {
		lit, ok := s[i].(*ast.FuncLit)
		if !ok {
			continue
		}
		for _, p := range flattenParams(lit.Type) {
			if p.Name == name {
				return true
			}
		}
	}
	return false
}

// sinkCall reports whether call is a library or helper sink, returning the
// sink name, the asserted error expression and whether the message is bare.
func (fc *fileCtx) sinkCall(call *ast.CallExpr) (string, ast.Expr, bool, bool) {
	if sink, e, bare, ok := fc.librarySink(call); ok {
		return sink, e, bare, true
	}
	return fc.helperCall(call)
}

// helperCall reports whether call is a call to a helper and, if so, returns
// the helper's name, the error expression passed to it and whether the call
// is bare.
func (fc *fileCtx) helperCall(call *ast.CallExpr) (string, ast.Expr, bool, bool) {
	if fc.mod == nil {
		return "", nil, false, false
	}
	fi := fc.mod.lookupFunc(fc, call)
	if fi == nil || fi.Helper == nil {
		return "", nil, false, false
	}
	h := fi.Helper
	name := "helper:" + helperName(fi)
	// Multi-value call: must(f()) or check(t, f()) is not valid Go, so the
	// only form is a single call argument supplying every parameter.
	if len(call.Args) == 1 && len(fi.Params) > 1 && call.Ellipsis == 0 {
		if inner, ok := ast.Unparen(call.Args[0]).(*ast.CallExpr); ok {
			return name, inner, h.Bare, true
		}
	}
	if h.ErrParam >= len(call.Args) {
		return "", nil, false, false
	}
	e := ast.Unparen(call.Args[h.ErrParam])
	bare := h.Bare
	for j := range h.MsgParams {
		var args []ast.Expr
		switch {
		case j >= len(call.Args):
		case fi.Params[j].Variadic:
			args = call.Args[j:]
		default:
			args = call.Args[j : j+1]
		}
		if !fc.bareArgs(Message{Args: args}, errName(e)) {
			bare = false
		}
	}
	return name, e, bare, true
}

// helperName returns a readable name for a helper: pkgpath.Func or
// pkgpath.(Recv).Method.
func helperName(fi *FuncInfo) string {
	prefix := fi.File.Pkg.Path
	if prefix == "" {
		prefix = fi.File.Pkg.Dir
	}
	d := fi.Decl
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return prefix + "." + d.Name.Name
	}
	typ := d.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	recv := "?"
	switch t := typ.(type) {
	case *ast.Ident:
		recv = t.Name
	case *ast.IndexExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			recv = id.Name
		}
	case *ast.IndexListExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			recv = id.Name
		}
	}
	return prefix + ".(" + recv + ")." + d.Name.Name
}

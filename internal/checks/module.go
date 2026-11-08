package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// Module is a parsed source tree: a repository or a module zip. It may
// contain several Go modules.
type Module struct {
	Fset  *token.FileSet
	Files []*File
	// ParseErrors counts files that failed to parse, and TestParseErrors
	// lists the _test.go files among them.
	ParseErrors     int
	TestParseErrors []string

	pkgs   map[pkgKey]*Package
	byPath map[string]*Package // import path -> non-test package
}

// File is one parsed Go file.
type File struct {
	Name string // slash-separated path relative to the root
	Src  []byte
	AST  *ast.File
	Pkg  *Package
	Test bool // a _test.go file
}

type pkgKey struct{ dir, name string }

// Package is the set of files in one directory with one package name. The
// _test.go files of package p are part of p; package p_test is separate.
type Package struct {
	Dir   string // slash-separated, relative to the root
	Name  string
	Path  string // import path, "" if unknown
	Files []*File

	funcs    map[string]*FuncInfo
	methods  map[string][]*FuncInfo
	structs  map[string]*ast.StructType
	closures map[*ast.Object]*FuncInfo
	all      []*FuncInfo
	byNode   map[ast.Node]*FuncInfo
	imports  map[string]bool // every path imported by a file in the package
}

// FuncInfo is a function or method declaration, or a function literal
// assigned to a local variable (a closure helper such as
// check := func(err error) {...}).
type FuncInfo struct {
	// Decl is the function declaration, or for a closure, the top-level
	// declaration that contains it.
	Decl *ast.FuncDecl
	// Lit and Var are set for closures: the literal and the variable name.
	Lit    *ast.FuncLit
	Var    string
	File   *File
	Params []Param
	Helper *HelperInfo
}

// Body returns the function body.
func (fi *FuncInfo) Body() *ast.BlockStmt {
	if fi.Lit != nil {
		return fi.Lit.Body
	}
	return fi.Decl.Body
}

// Node returns the function node: the literal for a closure, otherwise the
// declaration.
func (fi *FuncInfo) Node() ast.Node {
	if fi.Lit != nil {
		return fi.Lit
	}
	return fi.Decl
}

// Param is one flattened function parameter.
type Param struct {
	Name     string
	Type     ast.Expr
	Variadic bool
}

var generatedRe = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// Load parses every Go file under root. fallbackPath is used as the module
// path for files that are not inside any go.mod (GOPATH-era repositories).
func Load(root, fallbackPath string) (*Module, error) {
	m := &Module{
		Fset:   token.NewFileSet(),
		pkgs:   map[pkgKey]*Package{},
		byPath: map[string]*Package{},
	}
	modPaths := map[string]string{} // dir -> module path
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case name == "go.mod":
			data, err := os.ReadFile(p)
			if err == nil {
				if mp := modfile.ModulePath(data); mp != "" {
					modPaths[path.Dir(rel)] = mp
				}
			}
		case strings.HasSuffix(name, ".go"):
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if generatedRe.Match(src) {
			continue
		}
		f, err := parser.ParseFile(m.Fset, rel, src, parser.ParseComments)
		if err != nil {
			m.ParseErrors++
			if strings.HasSuffix(rel, "_test.go") {
				m.TestParseErrors = append(m.TestParseErrors, rel)
			}
			continue
		}
		dir := path.Dir(rel)
		key := pkgKey{dir, f.Name.Name}
		pkg := m.pkgs[key]
		if pkg == nil {
			pkg = &Package{
				Dir:      dir,
				Name:     f.Name.Name,
				Path:     importPath(dir, modPaths, fallbackPath),
				funcs:    map[string]*FuncInfo{},
				methods:  map[string][]*FuncInfo{},
				structs:  map[string]*ast.StructType{},
				closures: map[*ast.Object]*FuncInfo{},
				byNode:   map[ast.Node]*FuncInfo{},
				imports:  map[string]bool{},
			}
			m.pkgs[key] = pkg
			if pkg.Path != "" && !strings.HasSuffix(pkg.Name, "_test") && pkg.Name != "main" {
				m.byPath[pkg.Path] = pkg
			}
		}
		file := &File{Name: rel, Src: src, AST: f, Pkg: pkg, Test: strings.HasSuffix(rel, "_test.go")}
		pkg.Files = append(pkg.Files, file)
		m.Files = append(m.Files, file)
		pkg.index(file)
	}
	return m, nil
}

// importPath returns the import path of dir using the nearest enclosing
// go.mod, or fallback if there is none.
func importPath(dir string, modPaths map[string]string, fallback string) string {
	for d := dir; ; d = path.Dir(d) {
		if mp, ok := modPaths[d]; ok {
			return joinPath(mp, dir, d)
		}
		if d == "." || d == "/" {
			break
		}
	}
	if fallback == "" {
		return ""
	}
	return joinPath(fallback, dir, ".")
}

func joinPath(modPath, dir, modDir string) string {
	if dir == modDir {
		return modPath
	}
	rel := dir
	if modDir != "." {
		rel = strings.TrimPrefix(dir, modDir+"/")
	}
	if modPath == "std" {
		// The standard library module: packages are imported by their
		// path relative to src.
		return rel
	}
	return modPath + "/" + rel
}

// index records the declarations and imports of a file.
func (p *Package) index(f *File) {
	for _, imp := range f.AST.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil {
			p.imports[path] = true
		}
	}
	for _, decl := range f.AST.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			fi := &FuncInfo{Decl: d, File: f, Params: flattenParams(d.Type)}
			p.all = append(p.all, fi)
			p.byNode[d] = fi
			if d.Recv == nil {
				p.funcs[d.Name.Name] = fi
			} else {
				p.methods[d.Name.Name] = append(p.methods[d.Name.Name], fi)
			}
			p.indexClosures(f, d)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					p.structs[ts.Name.Name] = st
				}
			}
		}
	}
}

// indexClosures records function literals assigned to local variables in a
// declaration body: name := func(...) {...}, var name = func(...) {...}.
func (p *Package) indexClosures(f *File, d *ast.FuncDecl) {
	add := func(id *ast.Ident, rhs ast.Expr) {
		lit, ok := rhs.(*ast.FuncLit)
		if !ok || id.Obj == nil || id.Name == "_" {
			return
		}
		fi := &FuncInfo{Decl: d, Lit: lit, Var: id.Name, File: f, Params: flattenParams(lit.Type)}
		p.closures[id.Obj] = fi
		p.all = append(p.all, fi)
		p.byNode[lit] = fi
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE && len(n.Lhs) == len(n.Rhs) {
				for i, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						add(id, n.Rhs[i])
					}
				}
			}
		case *ast.ValueSpec:
			if len(n.Names) == len(n.Values) {
				for i, id := range n.Names {
					add(id, n.Values[i])
				}
			}
		}
		return true
	})
}

func flattenParams(ft *ast.FuncType) []Param {
	var params []Param
	if ft.Params == nil {
		return nil
	}
	for _, f := range ft.Params.List {
		typ := f.Type
		variadic := false
		if el, ok := typ.(*ast.Ellipsis); ok {
			typ, variadic = el.Elt, true
		}
		if len(f.Names) == 0 {
			params = append(params, Param{Name: "_", Type: typ, Variadic: variadic})
		}
		for _, n := range f.Names {
			params = append(params, Param{Name: n.Name, Type: typ, Variadic: variadic})
		}
	}
	return params
}

// lookupFunc resolves the function called by call from a file in package p.
// It handles unqualified calls to functions in the same package (or, for
// package p_test, in package p), qualified calls to packages in the module,
// and method calls whose name is unique in the package.
func (m *Module) lookupFunc(fc *fileCtx, call *ast.CallExpr) *FuncInfo {
	p := fc.pkg
	if p == nil {
		return nil
	}
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.IndexExpr:
		// Explicit instantiation: must[int](...).
		if id, ok := fun.X.(*ast.Ident); ok {
			return m.localFunc(p, id.Name)
		}
	case *ast.Ident:
		if fun.Obj != nil && fun.Obj.Kind == ast.Var {
			return p.closures[fun.Obj]
		}
		if fun.Obj != nil && fun.Obj.Kind != ast.Fun {
			return nil
		}
		if fi := m.localFunc(p, fun.Name); fi != nil {
			return fi
		}
		// A function from a dot-imported package in the module.
		for _, path := range fc.dotImports {
			if q := m.byPath[path]; q != nil {
				if fi := q.funcs[fun.Name]; fi != nil {
					return fi
				}
			}
		}
		return nil
	case *ast.SelectorExpr:
		if path := fc.pkgOf(fun.X); path != "" {
			if q := m.byPath[path]; q != nil {
				return q.funcs[fun.Sel.Name]
			}
			return nil
		}
		cands := p.methods[fun.Sel.Name]
		if len(cands) == 1 {
			return cands[0]
		}
	}
	return nil
}

func (m *Module) localFunc(p *Package, name string) *FuncInfo {
	if fi := p.funcs[name]; fi != nil {
		return fi
	}
	if base, ok := strings.CutSuffix(p.Name, "_test"); ok {
		if q := m.pkgs[pkgKey{p.Dir, base}]; q != nil {
			return q.funcs[name]
		}
	}
	return nil
}

package checks

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// isErrorName reports whether an identifier name counts as an error value:
// err, err followed by an upper-case letter or digit, or a name ending in Err.
func isErrorName(name string) bool {
	if name == "err" {
		return true
	}
	if rest, ok := strings.CutPrefix(name, "err"); ok && rest != "" {
		c := rest[0]
		return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	return len(name) > 3 && strings.HasSuffix(name, "Err")
}

// errorValue returns the identifier of e if e is an error value.
func errorValue(e ast.Expr) (*ast.Ident, bool) {
	id, ok := ast.Unparen(e).(*ast.Ident)
	if !ok || !isErrorName(id.Name) {
		return nil, false
	}
	return id, true
}

// isErr reports whether e is the error named name, or name.Error().
func isErr(e ast.Expr, name string) bool {
	e = ast.Unparen(e)
	if id, ok := e.(*ast.Ident); ok {
		return id.Name == name
	}
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 0 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
			if id, ok := ast.Unparen(sel.X).(*ast.Ident); ok {
				return id.Name == name
			}
		}
	}
	return false
}

// bareWords is the fixed vocabulary of words that add no information to an
// error message.
var bareWords = map[string]bool{
	"a": true, "an": true, "did": true, "err": true, "error": true,
	"errors": true, "expected": true, "fail": true, "failed": true,
	"failure": true, "fatal": true, "got": true, "have": true, "nil": true,
	"no": true, "not": true, "occurred": true, "returned": true,
	"should": true, "unexpected": true, "unexpectedly": true, "want": true,
}

var (
	verbRe = regexp.MustCompile(`%[-+# 0]*(\*|\d+)?(\.(\*|\d+)?)?(\[\d+\])?[a-zA-Z%]`)
	wordRe = regexp.MustCompile(`[A-Za-z0-9_]+`)
)

// bareText reports whether a message literal contains only bare words once
// formatting verbs and punctuation are removed.
func bareText(s string) bool {
	s = verbRe.ReplaceAllString(s, " ")
	for _, w := range wordRe.FindAllString(s, -1) {
		if !bareWords[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// stringLit returns the value of a string literal, including constant
// concatenations of literals.
func stringLit(e ast.Expr) (string, bool) {
	switch e := ast.Unparen(e).(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok1 := stringLit(e.X)
		y, ok2 := stringLit(e.Y)
		return x + y, ok1 && ok2
	}
	return "", false
}

// Message describes the arguments of a call that build a failure message.
type Message struct {
	Args []ast.Expr
	// Format is true if Args[0] is a format string (Fatalf, Errorf, testify
	// msgAndArgs and so on).
	Format bool
}

// bareArgs reports whether message arguments add nothing beyond the error
// named errName. Each argument must be a bare string literal or the error.
// While a helper body is being analysed, any string literal is allowed (a
// fixed message in a helper is the same at every call site), and fc.allowed
// reports helper parameters that reach the message.
func (fc *fileCtx) bareArgs(m Message, errName string) bool {
	for _, a := range m.Args {
		if s, ok := stringLit(a); ok {
			if !fc.helperMode && !bareText(s) {
				return false
			}
			continue
		}
		if errName != "" && isErr(a, errName) {
			continue
		}
		if call, ok := ast.Unparen(a).(*ast.CallExpr); ok && fc.isPkgFunc(call, "fmt", "Errorf") {
			if fc.bareArgs(Message{Args: call.Args, Format: true}, errName) {
				continue
			}
			return false
		}
		if fc.allowed != nil && fc.allowed(a) {
			continue
		}
		return false
	}
	return true
}

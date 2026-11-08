# testmsg-survey

A survey of how Go tests report errors in practice.

## Objective

The Go team has long declined assertion helpers in `testing`, on the grounds
that tests should write informative failure messages by hand
([FAQ](https://go.dev/doc/faq#testing_framework)). The same argument was used
against `testing.T.Must[T any](v T, err error) T` in
[golang/go#54297](https://github.com/golang/go/issues/54297): it would
"encourage lazy testing habits" and blur the line between "we know this
cannot happen" and "we are testing that this does not happen".

Our hypothesis is that hand-written failure messages are, in practice, mostly
bare: `if err != nil { t.Fatal(err) }`. This holds even in well-maintained
projects, including the Go project's own repositories. If so, the trade-off
the Go team describes doesn't exist in practice. The recommended style is
already rare, and helpers like `t.Must` would not make failure messages worse
than they are today.

The goal is to measure this across a large corpus of real-world code and
publish the method, the data and the results, so they can be cited in a Go
proposal.

## Questions

1. In `_test.go` files, how often does an error check report only the error,
   compared with adding context?
2. Does that differ between checks on test setup (temp dirs, fixtures,
   parsing constant inputs) and checks on the code under test?
3. Does it differ between the Go project's own code, popular third-party
   projects and the long tail?

## Definitions

These are fixed before any data is collected, so the classification can't be
argued after the fact.

### Error values

Without type information, an expression counts as an error value if it is an
identifier named `err`, starting with `err` followed by an upper-case letter
or digit (`errClose`), or ending in `Err` (`closeErr`). The type-checked pass
measures how much this misses.

### Sinks

A **sink** is a call that fails, skips or aborts a test when an error is
non-nil. Every sink has a **mechanism** tag.

- **direct**: an `if` statement whose condition is exactly `X != nil` (or
  `nil != X`) where `X` is an error value, and whose body contains, as a
  top-level statement, a call to `Fatal`, `Fatalf`, `Error`, `Errorf`,
  `Skip`, `Skipf`, `Fail` or `FailNow` on a testing value. A testing value
  is an identifier declared as `*testing.T`, `*testing.B`, `*testing.F` or
  `testing.TB` in an enclosing function, a call `X.T()` (testify suites), a
  field of testing type on the enclosing method's receiver (`h.t`), or the
  receiver itself when its struct embeds a testing type.
  `Log` and `Logf` calls at the top level of the same body count toward the
  message. Compound conditions such as `err != nil && !os.IsNotExist(err)`
  are not sinks.
- **library**: a call to one of the assertion functions listed below. For
  functions whose asserted parameter has type `error` (`NoError`,
  `NoErrorf`, `NilError`, `NoErr`, `NilErr`, `CmpNoError`, `RequireNoError`,
  and gomega's `Succeed`), any expression counts, including a call such as
  `require.NoError(t, f())`. For functions that accept any value (`Nil`,
  `Assert`, `Check`, `IsNil`, `BeNil`, `ShouldBeNil`), the asserted argument
  must be an error value.
- **panic**: `panic(X)`, `log.Fatal`, `log.Fatalf`, `log.Panic` or
  `log.Panicf` in an `if X != nil` body, with `X` an error value.
- **helper**: a call to a function that passes one of its parameters,
  unchanged, to a sink as the asserted error. The parameter may arrive as
  part of a multi-value call such as `must(t, f())`. Helpers can call other
  helpers. The set is computed as a fixed point over every `.go` file in the
  module, including non-test files (for example `internal/testutil`). Imports
  within the module are resolved to directories using the `go.mod` module
  path, or the repository path for code with no `go.mod`. A parameter is
  forwarded unchanged only if it is never assigned or redeclared in the
  helper body. Methods are resolved by name when exactly one method of that
  name is declared in the package. Calls to helpers outside the module are
  not resolved. A sink inside a helper body that checks the forwarded
  parameter is not counted as a check site of its own; its callers are.

Library sinks, matched by import path so aliased imports resolve:

| Library | Sinks | Message arguments |
| --- | --- | --- |
| `github.com/stretchr/testify/{assert,require}` | `NoError`, `NoErrorf`, `Nil`, `Nilf`, as package functions or methods (`assert.New(t)`, suites, `s.Require()`) | `msgAndArgs` |
| `gotest.tools/assert`, `gotest.tools/v3/assert` | `NilError`; `Assert` and `Check` with an error value | `msgAndArgs` |
| `github.com/frankban/quicktest` | `Assert` and `Check` with `qt.IsNil` | `qt.Commentf` and other comments |
| `github.com/go-quicktest/qt` | `Assert` and `Check` with `qt.IsNil(X)` | `qt.Commentf` and other comments |
| `gopkg.in/check.v1` | `c.Assert` and `c.Check` with `IsNil` | `Commentf` and other comments |
| `github.com/onsi/gomega` | `Expect(X)` or `Ω(X)` with `NotTo`/`ToNot`/`ShouldNot(HaveOccurred())`, `To`/`Should(Succeed())`, `To`/`Should(BeNil())` | optional description |
| `github.com/matryer/is` | `NoErr` | a trailing comment on the same line |
| `github.com/smartystreets/goconvey/convey` | `So(X, ShouldBeNil)` | none |
| `github.com/alecthomas/assert/v2` | `NoError` | `msgAndArgs` |
| `github.com/shoenig/test`, `github.com/shoenig/test/must` | `NoError`, `Nil` | `must.Sprintf` and other settings |
| `github.com/maxatome/go-testdeep/td` | `CmpNoError`, `NoError`, `RequireNoError` | `args` |
| `github.com/carlmjohnson/be` | `NilErr` | none |

Methods called on a receiver of unknown type are matched by method name and
argument shape, and only in files that import the library.

### Classification

Every check site is classified as bare or contextual.

- **Bare**: nothing besides the error reaches the message. For each message
  argument that is a string literal (a format string, or a literal argument
  to `Fatal`, `Error` and so on), remove formatting verbs and punctuation.
  It is bare if every remaining word, compared case-insensitively, belongs to
  this list: `a`, `an`, `did`, `err`, `error`, `errors`, `expected`, `fail`,
  `failed`, `failure`, `fatal`, `got`, `have`, `nil`, `no`, `not`,
  `occurred`, `returned`, `should`, `unexpected`, `unexpectedly`, `want`.
  The only other arguments allowed are the error value and `X.Error()`. A
  sink with no message at all, such as `t.FailNow()`, is bare.
- **Contextual**: anything else, meaning the message names the operation or
  includes some value other than the error.
- **Wrapped errors**: `fmt.Errorf("...: %w", err)` passed to a sink is
  classified by its format string, using the same rule.
- **Helpers**: a helper call is bare if the sink inside the helper is bare,
  counting a fixed message in the helper (`t.Fatalf("setup failed: %v",
  err)`) as bare, and no argument other than the testing value and the error
  reaches the message.
- Only the message counts. Subtest names, `Convey`, `Describe` and `It`
  labels, and table case names printed elsewhere are not counted as context.

### Setup and code under test

Still to be defined. The analyser records the call that produced the error
(from the `if` initialiser, or the closest earlier assignment to the error
value in the same block) and, where it can be resolved, its package, so this
can be decided before the results are looked at.

### Files

The analyser reads `_test.go` files. It skips `vendor` and `testdata`
directories, directories whose names start with `.` or `_`, and generated
files (those with a `// Code generated ... DO NOT EDIT.` line).

### Coverage

Calls that pass an error value to an unresolved function are counted, as an
upper bound on the sinks the analyser misses.

## Corpus

- **Go project**: `golang/go` and the `golang.org/x/...` repositories.
- **Popular**: the top N Go repositories on GitHub by stars.
- **Long tail**: a random sample of modules from the module proxy index
  (`index.golang.org`), latest version of each.

Each source is pinned to a specific revision, and the list is checked in so
the results can be reproduced.

## Analyser

A Go program that parses `_test.go` files with `go/parser` and `go/ast` and
classifies each error check. Parsing alone avoids having to download
dependencies and type-check every module, which makes it practical to run
over a large corpus. The cost is that types are inferred from names and
shapes (for example, `t *testing.T` parameters). A type-checked pass with
`golang.org/x/tools/go/analysis` can be run on a subset to measure how much
accuracy the faster approach gives up.

## Output

- Per-repository counts of bare and contextual error checks.
- Aggregate results for each corpus tier.
- A random sample of classified checks, for manual review of the
  classifier's accuracy.

## Status

- Corpus: Go project and popular tiers done (`corpus/`). Long tail not
  started; it will be a seeded random sample of unique modules from a full
  crawl of `index.golang.org`.
- Analyser: in progress.

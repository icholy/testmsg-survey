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

- **Error check**: an `if` statement whose condition is `err != nil` (for any
  identifier of type `error`) and whose body calls `Fatal`, `Fatalf`,
  `Error`, `Errorf`, `Skip` or `Skipf` on a `*testing.T`, `*testing.B`,
  `*testing.F` or `testing.TB`.
- **Bare**: the reporting call's arguments are just the error, or a format
  string that adds no information beyond it, such as `"%v"`, `"%s"`,
  `"error: %v"` or `"unexpected error: %v"`.
- **Contextual**: anything else, meaning the message names the operation or
  includes some value other than the error.
- **Setup and code under test**: to be defined. A first approximation is
  whether the call that produced the error belongs to the package under
  test.

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

Planning.

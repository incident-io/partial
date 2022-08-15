# Partial

Using Go 1.18's generics, this models a _partial struct_:
```go
type MyStruct struct {
  Thing1 string
  Thing2 string
}

whole := MyStruct{
  Thing1: "hello",
}
partStruct := partial.New(whole).Without("Thing2")
```

## Why is this useful?

Sometimes you want to do partial updates, or partial matches on a struct. In the
above example `whole.Thing2` is initialised to `""`, but really we just _don't
know what it is_. By using a Partial that explicitly says "I only know about
`Thing1`", we can safely apply `partStruct` as a database update for example.

This is also useful for matching things in tests: you might not _care_ about the
value in `Thing2`, and just want to match on `Thing1`.

## Generators

Two generators are included. To use them, install them with
```shell
go install github.com/incident-io/partial/cmd/partial
```

Then add a `go:generate` comment to each package that contains relevant structs:
```go
//go:generate partial
```

Within that package, annotate each struct that you want a matcher or builder for
with:
```go
// partial:builder,matcher
type MyStruct struct {
  ...
}
```

### Builder
The builder generated lets you build up a partial of the given struct. For
example:
```go
//go:generate partial
package things

// partial:builder
type MyStruct struct {
  Thing1 string
  Thing2 string
}
```

will generate a builder that you can use like so:
```go
partStruct := things.MyStructBuilder(
  things.MyStructBuilder.Thing1("hello"),
)
```

Because the builder is generated, you get type checking and autocompletion.

### Matcher
The matcher produces Gomega matchers, that let you match on _part_ of the
struct. If we update the comment in the above example to
```go
// partial:builder,matcher
```

we can then match as follows:
```go
Expect(myStruct).To(things.MyStructMatcher(
  things.MyStructMatcher.Thing1("hello"),
))
```

This will ignore any value in `myStruct.Thing2`.

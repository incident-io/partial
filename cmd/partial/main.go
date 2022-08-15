package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/pkg/errors"
)

type codegenTarget struct {
	Package    string
	Filename   string
	Tags       []string
	Type       *doc.Type
	StructType *ast.StructType
}

func main() {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err.Error())
		return
	}

	runGeneration(dir)
}

func runGeneration(dir string) error {
	log.Print("removing existing *.genpartial.go files...")
	err := removeExistingGenFiles(dir)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	notCodegenFiles := func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), ".partialgen.go")
	}
	pkgs, err := parser.ParseDir(fset, dir, notCodegenFiles, parser.ParseComments)
	if err != nil {
		return err
	}

	findStruct := func(pkg *ast.Package, name string) *ast.StructType {
		var result *ast.StructType
		ast.Inspect(pkg, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.TypeSpec:
				if node.Name.String() == name {
					result, _ = node.Type.(*ast.StructType)
					return false
				}
			}

			return true
		})

		return result
	}

	targets := []*codegenTarget{}
	for pkgName, pkg := range pkgs {
		docPkg := doc.New(pkg, "", doc.AllDecls)
		for _, pkgType := range docPkg.Types {
			if strings.Contains(pkgType.Doc, "partial:") {
				codegenTags := regexp.MustCompile(`partial:(\S+)`).FindStringSubmatch(pkgType.Doc)[1]
				pos := fset.Position(pkgType.Decl.TokPos)
				structType := findStruct(pkg, pkgType.Name)

				if structType == nil {
					return errors.New(fmt.Sprintf("could not find struct for name %s referenced by file %s", pkgType.Name, pos.Filename))
				}

				targets = append(targets, &codegenTarget{
					Package:    pkgName,
					Filename:   pos.Filename,
					Tags:       strings.Split(codegenTags, ","),
					Type:       pkgType,
					StructType: structType,
				})
			}
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Filename < targets[j].Filename
	})

	// Buffer all codegen files so we don't partially write then to disk
	buffers := map[string]*bytes.Buffer{}

	for _, target := range targets {
		targetFilename := strings.TrimSuffix(target.Filename, ".go") + ".partialgen.go"
		buf, ok := buffers[targetFilename]
		if !ok {
			buf = bytes.NewBufferString(genPreamble(target.Package))
			buffers[targetFilename] = buf
		}

		for _, tag := range target.Tags {
			switch tag {
			case "builder":
				if err := genBuilder(buf, target); err != nil {
					return errors.Wrap(err, fmt.Sprintf("error generating builder for %s in %s", target.Type.Name, target.Filename))
				}

			case "matcher":
				if err := genMatcher(buf, target); err != nil {
					return errors.Wrap(err, fmt.Sprintf("error generating matcher for %s in %s", target.Type.Name, target.Filename))
				}

			default:
				return errors.New(fmt.Sprintf("unrecognised codegen tag for %s in %s: %s", target.Type.Name, target.Filename, tag))
			}
		}
	}

	log.Print("writing buffers")
	for fileName, buf := range buffers {
		log.Printf("=> %s", fileName)
		if err := ioutil.WriteFile(fileName, buf.Bytes(), 0644); err != nil {
			return err
		}
	}

	{
		log.Print("go add missing imports")
		cmd := exec.Command("goimports", "-w", dir)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
	}

	{
		log.Print("go fmt")
		cmd := exec.Command("gofmt", "-w", dir)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
	}

	return nil
}

func genPreamble(pkg string) string {
	return fmt.Sprintf(`// Code generated by github.com/incident-io/partial/gen, DO NOT EDIT.

package %s

`, pkg)
}

// removeExistingGenFiles removes all .partialgen.go files in the given directory, and should be
// run before we attempt to rebuild things.
func removeExistingGenFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourceFile := path.Join(dir, entry.Name())
		if strings.HasSuffix(sourceFile, ".partialgen.go") {
			if err := os.Remove(sourceFile); err != nil {
				return err
			}
		}
	}

	return nil
}

// typeNameFor turns an ast.Expr into Go code that references the expressions type.
func typeNameFor(expr ast.Expr) (string, error) {
	switch fieldType := expr.(type) {
	case *ast.Ident:
		return fieldType.Name, nil // string

	case *ast.StarExpr:
		childType, err := typeNameFor(fieldType.X)
		if err != nil {
			return "", errors.Wrap(err, "pointer type")
		}

		return "*" + childType, nil // *string

	case *ast.SelectorExpr:
		childType, err := typeNameFor(fieldType.X)
		if err != nil {
			return "", errors.Wrap(err, "selector type")
		}

		return fmt.Sprintf("%s.%s", childType, fieldType.Sel.Name), nil // null.String

	case *ast.ArrayType:
		childType, err := typeNameFor(fieldType.Elt)
		if err != nil {
			return "", errors.Wrap(err, "array type")
		}

		return fmt.Sprintf("[]%s", childType), nil // []string
	}

	return "", errors.New(fmt.Sprintf("unsupported expr type: %v", expr))
}

type structField struct {
	FieldName     string // ID
	FieldTypeName string // string
}

func getFieldsFor(target *codegenTarget) ([]*structField, error) {
	fields := []*structField{}
	for _, field := range target.StructType.Fields.List {
		// Embedded fields, we can't help here
		if len(field.Names) == 0 {
			continue
		}

		fieldName := field.Names[0].Name
		typeName, err := typeNameFor(field.Type)
		if err != nil {
			return nil, errors.Wrap(err, fmt.Sprintf("field %s on type %s", fieldName, target.Type.Name))
		}

		fields = append(fields, &structField{
			FieldName:     fieldName, // ID
			FieldTypeName: typeName,  // string
		})
	}

	return fields, nil
}

// Builder!

func genBuilder(buf *bytes.Buffer, target *codegenTarget) error {
	fields, err := getFieldsFor(target)
	if err != nil {
		return err
	}

	vars := builderTemplateVars{
		TypeName:            target.Type.Name,
		BuilderTypeName:     fmt.Sprintf("%sBuilder", target.Type.Name),
		BuilderFuncTypeName: fmt.Sprintf("%sBuilderFunc", target.Type.Name),
		Fields:              fields,
	}

	for _, field := range vars.Fields {
		if field.FieldTypeName != "string" {
			continue // not an ID field!
		}
		if field.FieldName == "ID" {
			vars.HasID = true
		}
		if field.FieldName == "OrganisationID" {
			vars.HasOrganisationID = true
		}
	}

	if err := builderTemplate.Execute(buf, vars); err != nil {
		return errors.Wrap(err, "executing template")
	}

	return nil
}

type builderTemplateVars struct {
	TypeName            string // APIKey
	BuilderTypeName     string // APIKeyBuilder
	BuilderFuncTypeName string // APIKeyBuilderFunc
	HasID               bool
	HasOrganisationID   bool
	Fields              []*structField
}

var builderTemplate = template.Must(template.New("builderTemplate").Funcs(sprig.TxtFuncMap()).Parse(`
{{ if .HasID }}
func (t {{ .TypeName }}) GetID() string {
	return t.ID
}
{{ end }}

{{ if .HasOrganisationID }}
func (t {{ .TypeName }}) GetOrganisationID() string {
	return t.OrganisationID
}
{{ end }}

// {{ .BuilderTypeName }} initialises a {{ .TypeName }} struct with fields from the given setters. Setters
// are applied first to last, with subsequent sets taking precedence.
var {{ .BuilderTypeName }} = {{ .BuilderFuncTypeName }}(func(opts ...func(*{{ .TypeName }}) []string) partial.Partial[{{ .TypeName }}] {
	apply := func(base {{ .TypeName }}) partial.Partial[{{ .TypeName }}] {
		model := partial.Partial[{{ .TypeName }}]{
			Subject: base,
			FieldNames: []string{},
		}
		for _, opt := range opts {
			model.FieldNames = append(model.FieldNames, opt(&model.Subject)...)
		}

		return model
	}

	model := apply({{ .TypeName }}{})
	model.SetApply(func(base {{ .TypeName }}) *{{ .TypeName }} {
		patched := apply(base).Subject
		return &patched
	})

	return model
})

type {{ .BuilderFuncTypeName }} func(opts ...func(*{{ .TypeName }}) []string) partial.Partial[{{ .TypeName }}]

{{ range .Fields }}
func (b {{ $.BuilderFuncTypeName }}) {{ .FieldName }}(value {{ .FieldTypeName }}) func(*{{ $.TypeName }}) []string {
	return func(subject *{{ $.TypeName }}) []string {
		subject.{{ .FieldName }} = value

		return []string{
			{{ quote .FieldName }},
		}
	}
}
{{ end }}
`))

// Matcher!

func genMatcher(buf *bytes.Buffer, target *codegenTarget) error {
	fields, err := getFieldsFor(target)
	if err != nil {
		return err
	}

	vars := matcherTemplateVars{
		TypeName:            target.Type.Name,
		MatcherTypeName:     fmt.Sprintf("%sMatcher", target.Type.Name),
		MatcherFuncTypeName: fmt.Sprintf("%sMatcherFunc", target.Type.Name),
		Fields:              fields,
	}

	if err := matcherTemplate.Execute(buf, vars); err != nil {
		return errors.Wrap(err, "executing template")
	}

	return nil
}

type matcherTemplateVars struct {
	TypeName            string // APIKey
	MatcherTypeName     string // APIKeyMatcher
	MatcherFuncTypeName string // APIKeyMatcherFunc
	Fields              []*structField
}

var matcherTemplate = template.Must(template.New("matcherTemplate").Funcs(sprig.TxtFuncMap()).Parse(`
// {{ .MatcherTypeName }} creates a Gomega matcher for {{ .TypeName }} against the given
// fields. Matchers are applied first to last, with subsequent matchers taking precedence.
var {{ .MatcherTypeName }} = {{ .MatcherFuncTypeName }}(func(opts ...func(*{{ .TypeName }}, *gstruct.Fields)) types.GomegaMatcher {
	fields := gstruct.Fields{}
	for _, opt := range opts {
		opt(nil, &fields)
	}

	return gstruct.PointTo(
		gstruct.MatchFields(gstruct.IgnoreExtras, fields),
	)
})

// Matcher is added to the base type, permitting other generic functions to build matchers
// from each of the matcher-setter functions.
func (b {{ .TypeName }}) Matcher(opts ...func(*{{ .TypeName }}, *gstruct.Fields)) types.GomegaMatcher {
	return {{ .MatcherTypeName }}(opts...)
}

type {{ .MatcherFuncTypeName }} func(opts ...func(*{{ .TypeName }}, *gstruct.Fields)) types.GomegaMatcher

type {{ .MatcherTypeName }}Matchers struct {}

// Match returns an interface with the same methods as the base matcher, but accepting
// GomegaMatcher parameters instead of the exact equality matches.
func (b {{ .MatcherFuncTypeName }}) Match() {{ .MatcherTypeName }}Matchers {
	return {{ .MatcherTypeName }}Matchers{}
}

{{ range .Fields }}
func (b {{ $.MatcherFuncTypeName }}) {{ .FieldName }}(value {{ .FieldTypeName }}) func(*{{ $.TypeName }}, *gstruct.Fields) {
	return func(_ *{{ $.TypeName }}, fields *gstruct.Fields) {
		(*fields)[{{ .FieldName | quote }}] = gomega.Equal(value)
	}
}

func (b {{ $.MatcherFuncTypeName }}) Match{{ .FieldName }}(value types.GomegaMatcher) func(*{{ $.TypeName }}, *gstruct.Fields) {
	return func(_ *{{ $.TypeName }}, fields *gstruct.Fields) {
		(*fields)[{{ .FieldName | quote }}] = value
	}
}

func (b {{ $.MatcherTypeName }}Matchers) {{ .FieldName }}(value types.GomegaMatcher) func(*{{ $.TypeName }}, *gstruct.Fields) {
	return func(_ *{{ $.TypeName }}, fields *gstruct.Fields) {
		(*fields)[{{ .FieldName | quote }}] = value
	}
}
{{ end }}
`))

package codeparser

import (
	"strings"
	"testing"
)

// helper: index symbols by ID.
func symbolsByID(syms []CodeSymbol) map[string]CodeSymbol {
	m := make(map[string]CodeSymbol, len(syms))
	for _, s := range syms {
		m[s.ID] = s
	}
	return m
}

// helper: collect relations as "from|type|to" triples.
func relationTriples(rels []CodeRelation) map[string]bool {
	m := make(map[string]bool, len(rels))
	for _, r := range rels {
		m[r.From+"|"+string(r.Type)+"|"+r.To] = true
	}
	return m
}

func TestGoParserLanguage(t *testing.T) {
	p := NewGoParser()
	if p.Language() != LanguageGo {
		t.Errorf("Language() = %q, want %q", p.Language(), LanguageGo)
	}
}

func TestGoParserSymbolsAndRelations(t *testing.T) {
	src := `package demo

import (
	"fmt"

	"github.com/example/lib"
)

type Greeter interface {
	Greet(name string) string
}

type Person struct {
	Name string
	Greeter
}

var defaultName = "world"

func Hello(name string) string {
	return fmt.Sprintf("hello %s", name)
}

func (p *Person) Greet(name string) string {
	return Hello(name)
}

func main() {
	p := &Person{Name: defaultName}
	p.Greet("x")
	lib.Do()
}
`
	p := NewGoParser()
	fr, err := p.ParseFile("demo.go", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile error = %v", err)
	}
	if fr.Error != nil {
		t.Fatalf("FileResult.Error = %v", fr.Error)
	}

	syms := symbolsByID(fr.Symbols)

	// File + Package symbols.
	fileSym, ok := syms["demo.go"]
	if !ok {
		t.Fatalf("missing File symbol, got IDs: %v", keys(syms))
	}
	if fileSym.Kind != KindFile || fileSym.Language != LanguageGo {
		t.Errorf("File symbol = %+v", fileSym)
	}
	pkgSym, ok := syms["demo.go:demo"]
	if !ok {
		t.Fatalf("missing Package symbol, got IDs: %v", keys(syms))
	}
	if pkgSym.Kind != KindPackage || pkgSym.Name != "demo" {
		t.Errorf("Package symbol = %+v", pkgSym)
	}

	// Struct with embedded interface.
	person, ok := syms["demo.go:demo.Person"]
	if !ok {
		t.Fatalf("missing Struct symbol, got IDs: %v", keys(syms))
	}
	if person.Kind != KindStruct || person.Name != "Person" {
		t.Errorf("Struct symbol = %+v", person)
	}

	// Interface.
	if _, ok := syms["demo.go:demo.Greeter"]; !ok {
		t.Errorf("missing Greeter symbol, got IDs: %v", keys(syms))
	}

	// Function with signature.
	hello, ok := syms["demo.go:demo.Hello"]
	if !ok {
		t.Fatalf("missing Function symbol, got IDs: %v", keys(syms))
	}
	if hello.Kind != KindFunction || hello.Name != "Hello" {
		t.Errorf("Function symbol = %+v", hello)
	}
	if !strings.Contains(hello.Signature, "func Hello(name string) string") {
		t.Errorf("Hello.Signature = %q", hello.Signature)
	}

	// Method with receiver.
	greet, ok := syms["demo.go:demo.Person.Greet"]
	if !ok {
		t.Fatalf("missing Method symbol, got IDs: %v", keys(syms))
	}
	if greet.Kind != KindMethod || greet.Name != "Greet" {
		t.Errorf("Method symbol = %+v", greet)
	}
	if greet.Attributes["receiver"] != "*Person" {
		t.Errorf("Greet receiver = %q, want %q", greet.Attributes["receiver"], "*Person")
	}

	// Variable.
	v, ok := syms["demo.go:demo.defaultName"]
	if !ok {
		t.Fatalf("missing Variable symbol, got IDs: %v", keys(syms))
	}
	if v.Kind != KindVariable || v.Name != "defaultName" {
		t.Errorf("Variable symbol = %+v", v)
	}

	// Imports.
	fmtImp, ok := syms[`demo.go:import.fmt`]
	if !ok {
		t.Fatalf("missing fmt Import symbol, got IDs: %v", keys(syms))
	}
	if fmtImp.Kind != KindImport || fmtImp.Attributes["path"] != "fmt" {
		t.Errorf("fmt Import symbol = %+v", fmtImp)
	}
	if _, ok := syms[`demo.go:import.github.com/example/lib`]; !ok {
		t.Errorf("missing lib Import symbol, got IDs: %v", keys(syms))
	}

	rels := relationTriples(fr.Relations)

	// CONTAINS.
	for _, want := range [][2]string{
		{"demo.go", "demo.go:demo"},
		{"demo.go:demo", "demo.go:demo.Person"},
		{"demo.go:demo", "demo.go:demo.Hello"},
		{"demo.go:demo", "demo.go:demo.Person.Greet"},
		{"demo.go:demo", "demo.go:demo.defaultName"},
		{"demo.go:demo", "demo.go:demo.Greeter"},
		{"demo.go:demo.Person", "demo.go:demo.Person.Greet"},
	} {
		key := want[0] + "|CONTAINS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// IMPORTS.
	for _, want := range [][2]string{
		{"demo.go:demo", "demo.go:import.fmt"},
		{"demo.go:demo", "demo.go:import.github.com/example/lib"},
	} {
		key := want[0] + "|IMPORTS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// INHERITS: Person embeds Greeter.
	if !rels["demo.go:demo.Person|INHERITS|demo.go:demo.Greeter"] {
		t.Errorf("missing INHERITS relation Person->Greeter, relations: %v", keys2(rels))
	}

	// CALLS: Greet -> Hello, Hello -> Sprintf, main -> Greet.
	for _, want := range [][2]string{
		{"demo.go:demo.Person.Greet", "demo.go:demo.Hello"},
		{"demo.go:demo.main", "demo.go:demo.Person.Greet"},
	} {
		key := want[0] + "|CALLS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// REFERENCES: main references defaultName variable.
	if !rels["demo.go:demo.main|REFERENCES|demo.go:demo.defaultName"] {
		t.Errorf("missing REFERENCES relation main->defaultName")
	}
}

func TestGoParserSyntaxError(t *testing.T) {
	p := NewGoParser()
	_, err := p.ParseFile("bad.go", []byte("package demo\n\nfunc {"))
	if err == nil {
		t.Fatal("ParseFile should return error for broken source")
	}
}

func keys(m map[string]CodeSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keys2(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

package codeparser

import (
	"strings"
	"testing"
)

// assertRelationEndpointsResolve is a graph-integrity regression check: every
// relation endpoint (From and To) must resolve to an emitted symbol, so the
// downstream graph store never sees a dangling reference.
func assertRelationEndpointsResolve(t *testing.T, fr *FileResult) {
	t.Helper()
	syms := symbolsByID(fr.Symbols)
	for _, r := range fr.Relations {
		if _, ok := syms[r.From]; !ok {
			t.Errorf("relation %s %s %s: From endpoint %q has no emitted symbol",
				r.From, r.Type, r.To, r.From)
		}
		if _, ok := syms[r.To]; !ok {
			t.Errorf("relation %s %s %s: To endpoint %q has no emitted symbol",
				r.From, r.Type, r.To, r.To)
		}
	}
}

func TestJSParserLanguage(t *testing.T) {
	p := NewJSParser()
	if p.Language() != LanguageJavaScript {
		t.Errorf("Language() = %q, want %q", p.Language(), LanguageJavaScript)
	}
}

func TestJSParserSymbolsAndRelations(t *testing.T) {
	src := `import { helper } from './lib';
import * as util from './util';

const defaultName = "world";

function greet(name) {
	return helper(name);
}

class Animal {
	constructor(name) {
		this.name = name;
	}
	speak() {
		return this.name;
	}
}

class Dog extends Animal {
	speak() {
		return greet(defaultName);
	}
}

const arrow = (x) => greet(x);

function main() {
	const d = new Dog("rex");
	d.speak();
	arrow(1);
	util.log("done");
}
`
	p := NewJSParser()
	fr, err := p.ParseFile("demo.js", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile error = %v", err)
	}
	if fr.Error != nil {
		t.Fatalf("FileResult.Error = %v", fr.Error)
	}

	syms := symbolsByID(fr.Symbols)

	// File + Module symbols.
	fileSym, ok := syms["demo.js"]
	if !ok {
		t.Fatalf("missing File symbol, got IDs: %v", keys(syms))
	}
	if fileSym.Kind != KindFile || fileSym.Language != LanguageJavaScript {
		t.Errorf("File symbol = %+v", fileSym)
	}
	modSym, ok := syms["demo.js:module"]
	if !ok {
		t.Fatalf("missing Module symbol, got IDs: %v", keys(syms))
	}
	if modSym.Kind != KindModule {
		t.Errorf("Module symbol = %+v", modSym)
	}

	// Function with signature.
	greet, ok := syms["demo.js:module.greet"]
	if !ok {
		t.Fatalf("missing greet Function symbol, got IDs: %v", keys(syms))
	}
	if greet.Kind != KindFunction || greet.Name != "greet" {
		t.Errorf("greet symbol = %+v", greet)
	}
	if !strings.Contains(greet.Signature, "function greet(name)") {
		t.Errorf("greet.Signature = %q", greet.Signature)
	}

	// Classes.
	animal, ok := syms["demo.js:module.Animal"]
	if !ok {
		t.Fatalf("missing Animal Class symbol, got IDs: %v", keys(syms))
	}
	if animal.Kind != KindClass || animal.Name != "Animal" {
		t.Errorf("Animal symbol = %+v", animal)
	}
	if _, ok := syms["demo.js:module.Dog"]; !ok {
		t.Errorf("missing Dog Class symbol, got IDs: %v", keys(syms))
	}

	// Methods.
	speak, ok := syms["demo.js:module.Animal.speak"]
	if !ok {
		t.Fatalf("missing Animal.speak Method symbol, got IDs: %v", keys(syms))
	}
	if speak.Kind != KindMethod || speak.Name != "speak" {
		t.Errorf("Animal.speak symbol = %+v", speak)
	}
	if _, ok := syms["demo.js:module.Dog.speak"]; !ok {
		t.Errorf("missing Dog.speak Method symbol, got IDs: %v", keys(syms))
	}

	// Variables.
	v, ok := syms["demo.js:module.defaultName"]
	if !ok {
		t.Fatalf("missing defaultName Variable symbol, got IDs: %v", keys(syms))
	}
	if v.Kind != KindVariable || v.Name != "defaultName" {
		t.Errorf("defaultName symbol = %+v", v)
	}
	arrow, ok := syms["demo.js:module.arrow"]
	if !ok {
		t.Fatalf("missing arrow Variable symbol, got IDs: %v", keys(syms))
	}
	if arrow.Kind != KindVariable || arrow.Attributes["value"] != "arrow_function" {
		t.Errorf("arrow symbol = %+v", arrow)
	}

	// Imports.
	libImp, ok := syms["demo.js:import../lib"]
	if !ok {
		t.Fatalf("missing ./lib Import symbol, got IDs: %v", keys(syms))
	}
	if libImp.Kind != KindImport || libImp.Attributes["path"] != "./lib" {
		t.Errorf("./lib Import symbol = %+v", libImp)
	}
	if _, ok := syms["demo.js:import../util"]; !ok {
		t.Errorf("missing ./util Import symbol, got IDs: %v", keys(syms))
	}

	rels := relationTriples(fr.Relations)

	// CONTAINS.
	for _, want := range [][2]string{
		{"demo.js", "demo.js:module"},
		{"demo.js:module", "demo.js:module.greet"},
		{"demo.js:module", "demo.js:module.Animal"},
		{"demo.js:module", "demo.js:module.Dog"},
		{"demo.js:module", "demo.js:module.defaultName"},
		{"demo.js:module", "demo.js:module.arrow"},
		{"demo.js:module", "demo.js:module.main"},
		{"demo.js:module.Animal", "demo.js:module.Animal.speak"},
		{"demo.js:module.Dog", "demo.js:module.Dog.speak"},
	} {
		key := want[0] + "|CONTAINS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// IMPORTS.
	for _, want := range [][2]string{
		{"demo.js:module", "demo.js:import../lib"},
		{"demo.js:module", "demo.js:import../util"},
	} {
		key := want[0] + "|IMPORTS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// INHERITS: Dog extends Animal.
	if !rels["demo.js:module.Dog|INHERITS|demo.js:module.Animal"] {
		t.Errorf("missing INHERITS relation Dog->Animal, relations: %v", keys2(rels))
	}

	// CALLS: Dog.speak->greet, arrow(var)->greet. The imported `helper` is
	// NOT a CALLS target: it has no emitted symbol in this file, so such an
	// edge would dangle (see the graph-integrity check below).
	for _, want := range [][2]string{
		{"demo.js:module.Dog.speak", "demo.js:module.greet"},
		{"demo.js:module.arrow", "demo.js:module.greet"},
	} {
		key := want[0] + "|CALLS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}
	if rels["demo.js:module.greet|CALLS|demo.js:module.helper"] {
		t.Error("unexpected dangling CALLS greet->helper: imported names have no symbol")
	}

	// REFERENCES: Dog.speak references defaultName variable.
	if !rels["demo.js:module.Dog.speak|REFERENCES|demo.js:module.defaultName"] {
		t.Errorf("missing REFERENCES relation Dog.speak->defaultName")
	}

	// Graph integrity: every relation endpoint resolves to an emitted symbol.
	assertRelationEndpointsResolve(t, fr)
}

func TestJSParserSyntaxError(t *testing.T) {
	p := NewJSParser()
	_, err := p.ParseFile("bad.js", []byte("function {"))
	if err == nil {
		t.Fatal("ParseFile should return error for broken source")
	}
}

func TestTSParserLanguage(t *testing.T) {
	p := NewTSParser()
	if p.Language() != LanguageTypeScript {
		t.Errorf("Language() = %q, want %q", p.Language(), LanguageTypeScript)
	}
}

func TestTSParserSymbolsAndRelations(t *testing.T) {
	src := `import { helper } from './lib';

interface Greeter {
	greet(name: string): string;
}

interface Named {
	name: string;
}

interface FriendlyGreeter extends Greeter, Named {
	bye(): void;
}

class Person implements FriendlyGreeter {
	name: string = "p";
	greet(name: string): string {
		return helper(name);
	}
	bye(): void {}
}

const defaultName: string = "world";

function main(): void {
	const p = new Person();
	p.greet(defaultName);
}
`
	p := NewTSParser()
	fr, err := p.ParseFile("demo.ts", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile error = %v", err)
	}
	if fr.Error != nil {
		t.Fatalf("FileResult.Error = %v", fr.Error)
	}

	syms := symbolsByID(fr.Symbols)

	// File + Module symbols.
	fileSym, ok := syms["demo.ts"]
	if !ok {
		t.Fatalf("missing File symbol, got IDs: %v", keys(syms))
	}
	if fileSym.Kind != KindFile || fileSym.Language != LanguageTypeScript {
		t.Errorf("File symbol = %+v", fileSym)
	}
	if _, ok := syms["demo.ts:module"]; !ok {
		t.Fatalf("missing Module symbol, got IDs: %v", keys(syms))
	}

	// Interfaces.
	greeter, ok := syms["demo.ts:module.Greeter"]
	if !ok {
		t.Fatalf("missing Greeter Class symbol, got IDs: %v", keys(syms))
	}
	if greeter.Kind != KindClass || greeter.Name != "Greeter" {
		t.Errorf("Greeter symbol = %+v", greeter)
	}
	if _, ok := syms["demo.ts:module.FriendlyGreeter"]; !ok {
		t.Errorf("missing FriendlyGreeter symbol, got IDs: %v", keys(syms))
	}

	// Interface method signature.
	if _, ok := syms["demo.ts:module.Greeter.greet"]; !ok {
		t.Errorf("missing Greeter.greet Method symbol, got IDs: %v", keys(syms))
	}

	// Class with implements.
	person, ok := syms["demo.ts:module.Person"]
	if !ok {
		t.Fatalf("missing Person Class symbol, got IDs: %v", keys(syms))
	}
	if person.Kind != KindClass || person.Name != "Person" {
		t.Errorf("Person symbol = %+v", person)
	}

	// Class method.
	greetM, ok := syms["demo.ts:module.Person.greet"]
	if !ok {
		t.Fatalf("missing Person.greet Method symbol, got IDs: %v", keys(syms))
	}
	if greetM.Kind != KindMethod || greetM.Name != "greet" {
		t.Errorf("Person.greet symbol = %+v", greetM)
	}

	// Variable.
	if _, ok := syms["demo.ts:module.defaultName"]; !ok {
		t.Errorf("missing defaultName Variable symbol, got IDs: %v", keys(syms))
	}

	// Import.
	if _, ok := syms["demo.ts:import../lib"]; !ok {
		t.Errorf("missing ./lib Import symbol, got IDs: %v", keys(syms))
	}

	rels := relationTriples(fr.Relations)

	// CONTAINS.
	for _, want := range [][2]string{
		{"demo.ts", "demo.ts:module"},
		{"demo.ts:module", "demo.ts:module.Greeter"},
		{"demo.ts:module", "demo.ts:module.Person"},
		{"demo.ts:module", "demo.ts:module.defaultName"},
		{"demo.ts:module", "demo.ts:module.main"},
		{"demo.ts:module.Greeter", "demo.ts:module.Greeter.greet"},
		{"demo.ts:module.Person", "demo.ts:module.Person.greet"},
	} {
		key := want[0] + "|CONTAINS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// IMPORTS.
	if !rels["demo.ts:module|IMPORTS|demo.ts:import../lib"] {
		t.Errorf("missing IMPORTS relation module->./lib")
	}

	// INHERITS: FriendlyGreeter extends Greeter & Named; Person implements
	// FriendlyGreeter.
	for _, want := range [][2]string{
		{"demo.ts:module.FriendlyGreeter", "demo.ts:module.Greeter"},
		{"demo.ts:module.FriendlyGreeter", "demo.ts:module.Named"},
		{"demo.ts:module.Person", "demo.ts:module.FriendlyGreeter"},
	} {
		key := want[0] + "|INHERITS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// CALLS: the imported `helper` is NOT a target (no emitted symbol in this
	// file). Person.greet has no other in-file call target.
	if rels["demo.ts:module.Person.greet|CALLS|demo.ts:module.helper"] {
		t.Error("unexpected dangling CALLS Person.greet->helper: imported names have no symbol")
	}

	// REFERENCES: main references defaultName variable.
	if !rels["demo.ts:module.main|REFERENCES|demo.ts:module.defaultName"] {
		t.Errorf("missing REFERENCES relation main->defaultName")
	}

	// Graph integrity: every relation endpoint resolves to an emitted symbol.
	assertRelationEndpointsResolve(t, fr)
}

func TestTSParserSyntaxError(t *testing.T) {
	p := NewTSParser()
	_, err := p.ParseFile("bad.ts", []byte("function {"))
	if err == nil {
		t.Fatal("ParseFile should return error for broken source")
	}
}

func TestJSTSParserByExtension(t *testing.T) {
	src := []byte("export const x: number = 1;\n")

	// .ts routes to the TypeScript parser.
	p, err := NewJSTSParserForPath("a.ts")
	if err != nil {
		t.Fatalf("NewJSTSParserForPath(a.ts) error = %v", err)
	}
	if p.Language() != LanguageTypeScript {
		t.Errorf("a.ts language = %q, want %q", p.Language(), LanguageTypeScript)
	}
	if _, err := p.ParseFile("a.ts", src); err != nil {
		t.Errorf("a.ts parse error = %v", err)
	}

	// .js routes to the JavaScript parser; TS-only syntax must fail there.
	p, err = NewJSTSParserForPath("a.js")
	if err != nil {
		t.Fatalf("NewJSTSParserForPath(a.js) error = %v", err)
	}
	if p.Language() != LanguageJavaScript {
		t.Errorf("a.js language = %q, want %q", p.Language(), LanguageJavaScript)
	}
	if _, err := p.ParseFile("a.js", src); err == nil {
		t.Error("a.js should fail to parse TS-only type annotation")
	}

	// .tsx / .jsx map to TypeScript / JavaScript respectively.
	for path, want := range map[string]Language{
		"b.tsx": LanguageTypeScript,
		"c.jsx": LanguageJavaScript,
		"d.mjs": LanguageJavaScript,
		"e.cjs": LanguageJavaScript,
	} {
		p, err := NewJSTSParserForPath(path)
		if err != nil {
			t.Fatalf("NewJSTSParserForPath(%s) error = %v", path, err)
		}
		if p.Language() != want {
			t.Errorf("%s language = %q, want %q", path, p.Language(), want)
		}
	}

	// Unknown extension is an error.
	if _, err := NewJSTSParserForPath("a.py"); err == nil {
		t.Error("NewJSTSParserForPath(a.py) should return error")
	}
}

// TestTSParserAngleBracketAssertion verifies that a plain .ts file parses
// angle-bracket type assertions (<T>x) as TypeScript, not as JSX. The .ts
// route must use the plain TS grammar; only .tsx uses the TSX grammar.
func TestTSParserAngleBracketAssertion(t *testing.T) {
	src := []byte("const n = <number>someValue;\n")
	p, err := NewJSTSParserForPath("assert.ts")
	if err != nil {
		t.Fatalf("NewJSTSParserForPath(assert.ts) error = %v", err)
	}
	fr, err := p.ParseFile("assert.ts", src)
	if err != nil {
		t.Fatalf("ParseFile(assert.ts) error = %v (angle-bracket assertion misread as JSX?)", err)
	}
	if fr.Error != nil {
		t.Fatalf("FileResult.Error = %v", fr.Error)
	}
	if _, ok := symbolsByID(fr.Symbols)["assert.ts:module.n"]; !ok {
		t.Errorf("missing variable symbol n, got IDs: %v", keys(symbolsByID(fr.Symbols)))
	}
}

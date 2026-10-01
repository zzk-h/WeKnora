package codeparser

import (
	"strings"
	"testing"
)

func TestPythonParserLanguage(t *testing.T) {
	p := NewPythonParser()
	if p.Language() != LanguagePython {
		t.Errorf("Language() = %q, want %q", p.Language(), LanguagePython)
	}
}

func TestPythonParserSymbolsAndRelations(t *testing.T) {
	src := `import os
import sys as system
from collections import OrderedDict

MAX_RETRIES = 3


class Animal:
    def speak(self):
        return "..."


class Dog(Animal):
    def speak(self):
        return "woof"


def helper(name):
    return "hello " + name


def greet(name):
    return helper(name)


def main():
    d = Dog()
    d.speak()
    greet("x")
    print(MAX_RETRIES)
    os.getcwd()
`
	p := NewPythonParser()
	fr, err := p.ParseFile("demo.py", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile error = %v", err)
	}
	if fr.Error != nil {
		t.Fatalf("FileResult.Error = %v", fr.Error)
	}

	syms := symbolsByID(fr.Symbols)

	// File + Module symbols.
	fileSym, ok := syms["demo.py"]
	if !ok {
		t.Fatalf("missing File symbol, got IDs: %v", keys(syms))
	}
	if fileSym.Kind != KindFile || fileSym.Language != LanguagePython {
		t.Errorf("File symbol = %+v", fileSym)
	}
	modSym, ok := syms["demo.py:demo"]
	if !ok {
		t.Fatalf("missing Module symbol, got IDs: %v", keys(syms))
	}
	if modSym.Kind != KindModule || modSym.Name != "demo" {
		t.Errorf("Module symbol = %+v", modSym)
	}

	// Classes.
	animal, ok := syms["demo.py:demo.Animal"]
	if !ok {
		t.Fatalf("missing Animal Class symbol, got IDs: %v", keys(syms))
	}
	if animal.Kind != KindClass || animal.Name != "Animal" {
		t.Errorf("Animal symbol = %+v", animal)
	}
	if _, ok := syms["demo.py:demo.Dog"]; !ok {
		t.Errorf("missing Dog Class symbol, got IDs: %v", keys(syms))
	}

	// Methods.
	dogSpeak, ok := syms["demo.py:demo.Dog.speak"]
	if !ok {
		t.Fatalf("missing Dog.speak Method symbol, got IDs: %v", keys(syms))
	}
	if dogSpeak.Kind != KindMethod || dogSpeak.Name != "speak" {
		t.Errorf("Dog.speak symbol = %+v", dogSpeak)
	}
	if _, ok := syms["demo.py:demo.Animal.speak"]; !ok {
		t.Errorf("missing Animal.speak Method symbol, got IDs: %v", keys(syms))
	}

	// Functions with signature.
	greet, ok := syms["demo.py:demo.greet"]
	if !ok {
		t.Fatalf("missing greet Function symbol, got IDs: %v", keys(syms))
	}
	if greet.Kind != KindFunction || greet.Name != "greet" {
		t.Errorf("greet symbol = %+v", greet)
	}
	if !strings.Contains(greet.Signature, "def greet(name)") {
		t.Errorf("greet.Signature = %q", greet.Signature)
	}

	// Module-level variable.
	v, ok := syms["demo.py:demo.MAX_RETRIES"]
	if !ok {
		t.Fatalf("missing Variable symbol, got IDs: %v", keys(syms))
	}
	if v.Kind != KindVariable || v.Name != "MAX_RETRIES" {
		t.Errorf("Variable symbol = %+v", v)
	}

	// Imports.
	osImp, ok := syms["demo.py:import.os"]
	if !ok {
		t.Fatalf("missing os Import symbol, got IDs: %v", keys(syms))
	}
	if osImp.Kind != KindImport || osImp.Attributes["path"] != "os" {
		t.Errorf("os Import symbol = %+v", osImp)
	}
	if _, ok := syms["demo.py:import.sys"]; !ok {
		t.Errorf("missing sys Import symbol, got IDs: %v", keys(syms))
	}
	if _, ok := syms["demo.py:import.collections"]; !ok {
		t.Errorf("missing collections Import symbol, got IDs: %v", keys(syms))
	}

	rels := relationTriples(fr.Relations)

	// CONTAINS.
	for _, want := range [][2]string{
		{"demo.py", "demo.py:demo"},
		{"demo.py:demo", "demo.py:demo.Animal"},
		{"demo.py:demo", "demo.py:demo.Dog"},
		{"demo.py:demo", "demo.py:demo.greet"},
		{"demo.py:demo", "demo.py:demo.MAX_RETRIES"},
		{"demo.py:demo.Animal", "demo.py:demo.Animal.speak"},
		{"demo.py:demo.Dog", "demo.py:demo.Dog.speak"},
	} {
		key := want[0] + "|CONTAINS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// IMPORTS.
	for _, want := range [][2]string{
		{"demo.py:demo", "demo.py:import.os"},
		{"demo.py:demo", "demo.py:import.sys"},
		{"demo.py:demo", "demo.py:import.collections"},
	} {
		key := want[0] + "|IMPORTS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// INHERITS: Dog(Animal).
	if !rels["demo.py:demo.Dog|INHERITS|demo.py:demo.Animal"] {
		t.Errorf("missing INHERITS relation Dog->Animal, relations: %v", keys2(rels))
	}

	// CALLS: greet -> helper, main -> greet, main -> Dog.speak.
	for _, want := range [][2]string{
		{"demo.py:demo.greet", "demo.py:demo.helper"},
		{"demo.py:demo.main", "demo.py:demo.greet"},
		{"demo.py:demo.main", "demo.py:demo.Dog.speak"},
	} {
		key := want[0] + "|CALLS|" + want[1]
		if !rels[key] {
			t.Errorf("missing relation %s", key)
		}
	}

	// REFERENCES: main references MAX_RETRIES variable.
	if !rels["demo.py:demo.main|REFERENCES|demo.py:demo.MAX_RETRIES"] {
		t.Errorf("missing REFERENCES relation main->MAX_RETRIES")
	}
}

func TestPythonParserSyntaxError(t *testing.T) {
	p := NewPythonParser()
	_, err := p.ParseFile("bad.py", []byte("def broken(:\n    pass"))
	if err == nil {
		t.Fatal("ParseFile should return error for broken source")
	}
}

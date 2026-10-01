package codeparser

import "testing"

func TestCodeSymbolModel(t *testing.T) {
	r := Range{
		Start: Position{Line: 3, Column: 1},
		End:   Position{Line: 10, Column: 2},
	}
	sym := CodeSymbol{
		ID:        "main.go:main",
		Kind:      KindFunction,
		Name:      "main",
		File:      "main.go",
		Range:     r,
		Signature: "func main()",
		Language:  LanguageGo,
		Attributes: map[string]string{
			"package": "main",
		},
	}

	if sym.ID != "main.go:main" {
		t.Errorf("ID = %q, want %q", sym.ID, "main.go:main")
	}
	if sym.Kind != KindFunction {
		t.Errorf("Kind = %q, want %q", sym.Kind, KindFunction)
	}
	if sym.Language != LanguageGo {
		t.Errorf("Language = %q, want %q", sym.Language, LanguageGo)
	}
	if sym.Range.Start.Line != 3 || sym.Range.End.Column != 2 {
		t.Errorf("Range = %+v, want %+v", sym.Range, r)
	}
	if sym.Attributes["package"] != "main" {
		t.Errorf("Attributes[package] = %q, want %q", sym.Attributes["package"], "main")
	}
}

func TestCodeRelationTypes(t *testing.T) {
	types := []RelationType{
		RelationContains,
		RelationCalls,
		RelationImports,
		RelationInherits,
		RelationReferences,
	}
	want := []string{"CONTAINS", "CALLS", "IMPORTS", "INHERITS", "REFERENCES"}
	for i, rt := range types {
		if string(rt) != want[i] {
			t.Errorf("RelationType = %q, want %q", string(rt), want[i])
		}
	}

	rel := CodeRelation{From: "a.go:pkg", To: "b.go:pkg", Type: RelationImports}
	if rel.From != "a.go:pkg" || rel.To != "b.go:pkg" || rel.Type != RelationImports {
		t.Errorf("CodeRelation = %+v", rel)
	}
}

func TestFileResultErrorDegradation(t *testing.T) {
	fr := FileResult{
		File:  "bad.go",
		Error: &ParseError{File: "bad.go", Message: "syntax error"},
	}
	if fr.Error == nil {
		t.Fatal("Error should not be nil")
	}
	if fr.Error.File != "bad.go" {
		t.Errorf("Error.File = %q, want %q", fr.Error.File, "bad.go")
	}
	if fr.Error.Error() == "" {
		t.Error("ParseError.Error() should be non-empty")
	}

	ok := FileResult{File: "ok.go", Symbols: []CodeSymbol{{ID: "ok.go:pkg", Kind: KindPackage}}}
	if ok.Error != nil {
		t.Errorf("Error = %v, want nil", ok.Error)
	}
}

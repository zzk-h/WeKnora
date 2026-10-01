package codeparser

import (
	"errors"
	"testing"
)

// stubParser is a test double for the Parser seam.
type stubParser struct {
	lang Language
	fn   func(path string, src []byte) (*FileResult, error)
}

func (s *stubParser) Language() Language { return s.lang }

func (s *stubParser) ParseFile(path string, src []byte) (*FileResult, error) {
	return s.fn(path, src)
}

func TestParserInterface(t *testing.T) {
	var p Parser = &stubParser{
		lang: LanguageGo,
		fn: func(path string, src []byte) (*FileResult, error) {
			return &FileResult{
				File:    path,
				Symbols: []CodeSymbol{{ID: path + ":pkg", Kind: KindPackage, Name: "pkg"}},
			}, nil
		},
	}

	if p.Language() != LanguageGo {
		t.Errorf("Language() = %q, want %q", p.Language(), LanguageGo)
	}

	fr, err := p.ParseFile("a.go", []byte("package pkg"))
	if err != nil {
		t.Fatalf("ParseFile error = %v", err)
	}
	if fr.File != "a.go" {
		t.Errorf("FileResult.File = %q, want %q", fr.File, "a.go")
	}
	if len(fr.Symbols) != 1 || fr.Symbols[0].Kind != KindPackage {
		t.Errorf("FileResult.Symbols = %+v", fr.Symbols)
	}
}

func TestParseBatchSuccess(t *testing.T) {
	p := &stubParser{
		lang: LanguageGo,
		fn: func(path string, src []byte) (*FileResult, error) {
			return &FileResult{
				File:    path,
				Symbols: []CodeSymbol{{ID: path + ":pkg", Kind: KindPackage, Name: "pkg"}},
			}, nil
		},
	}

	res := ParseBatch(p, []SourceFile{
		{Path: "a.go", Source: []byte("package pkg")},
		{Path: "b.go", Source: []byte("package pkg")},
	})

	if len(res.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(res.Files))
	}
	for _, fr := range res.Files {
		if fr.Error != nil {
			t.Errorf("Files[%s].Error = %v, want nil", fr.File, fr.Error)
		}
		if len(fr.Symbols) != 1 {
			t.Errorf("Files[%s].Symbols = %+v", fr.File, fr.Symbols)
		}
	}
}

func TestParseBatchSingleFileFailureDegrades(t *testing.T) {
	p := &stubParser{
		lang: LanguageGo,
		fn: func(path string, src []byte) (*FileResult, error) {
			if path == "bad.go" {
				return nil, errors.New("boom")
			}
			return &FileResult{
				File:    path,
				Symbols: []CodeSymbol{{ID: path + ":pkg", Kind: KindPackage, Name: "pkg"}},
			}, nil
		},
	}

	res := ParseBatch(p, []SourceFile{
		{Path: "good.go", Source: []byte("package pkg")},
		{Path: "bad.go", Source: []byte("package ")},
		{Path: "also_good.go", Source: []byte("package pkg")},
	})

	if len(res.Files) != 3 {
		t.Fatalf("len(Files) = %d, want 3", len(res.Files))
	}

	if res.Files[0].Error != nil {
		t.Errorf("good.go Error = %v, want nil", res.Files[0].Error)
	}
	if res.Files[1].Error == nil {
		t.Fatal("bad.go Error should not be nil")
	}
	if res.Files[1].Error.File != "bad.go" {
		t.Errorf("bad.go Error.File = %q, want %q", res.Files[1].Error.File, "bad.go")
	}
	if res.Files[1].Error.Message == "" {
		t.Error("bad.go Error.Message should be non-empty")
	}
	if res.Files[2].Error != nil {
		t.Errorf("also_good.go Error = %v, want nil", res.Files[2].Error)
	}
	if len(res.Files[2].Symbols) != 1 {
		t.Errorf("also_good.go Symbols = %+v", res.Files[2].Symbols)
	}
}

// Package codeparser provides language-agnostic code parsing built on
// tree-sitter. It turns source files into structured code symbols and
// relations (CONTAINS / CALLS / IMPORTS / INHERITS / REFERENCES) that can be
// consumed uniformly across languages.
package codeparser

import "fmt"

// Language identifies the source language of a parsed file.
type Language string

// Supported languages.
const (
	LanguageGo         Language = "go"
	LanguageJavaScript Language = "javascript"
	LanguageTypeScript Language = "typescript"
	LanguagePython     Language = "python"
)

// SymbolKind classifies a code symbol.
type SymbolKind string

// Supported symbol kinds.
const (
	KindFile     SymbolKind = "File"
	KindPackage  SymbolKind = "Package"
	KindModule   SymbolKind = "Module"
	KindFunction SymbolKind = "Function"
	KindMethod   SymbolKind = "Method"
	KindStruct   SymbolKind = "Struct"
	KindClass    SymbolKind = "Class"
	KindVariable SymbolKind = "Variable"
	KindImport   SymbolKind = "Import"
)

// RelationType classifies a relation between two symbols.
type RelationType string

// Supported relation types.
const (
	RelationContains   RelationType = "CONTAINS"
	RelationCalls      RelationType = "CALLS"
	RelationImports    RelationType = "IMPORTS"
	RelationInherits   RelationType = "INHERITS"
	RelationReferences RelationType = "REFERENCES"
)

// Position is a 0-based line/column position in a source file.
type Position struct {
	Line   uint32 `json:"line"`
	Column uint32 `json:"column"`
}

// Range is a half-open source range [Start, End).
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// CodeSymbol is a language-agnostic code symbol with a stable ID.
type CodeSymbol struct {
	ID         string            `json:"id"`
	Kind       SymbolKind        `json:"kind"`
	Name       string            `json:"name"`
	File       string            `json:"file"`
	Range      Range             `json:"range"`
	Signature  string            `json:"signature,omitempty"`
	Language   Language          `json:"language"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// CodeRelation is a directed relation between two symbols.
type CodeRelation struct {
	From string       `json:"from"`
	To   string       `json:"to"`
	Type RelationType `json:"type"`
}

// ParseError describes a per-file parse failure. A failed file degrades to an
// error result without aborting the rest of the batch.
type ParseError struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("codeparser: %s: %s", e.File, e.Message)
}

// FileResult is the parse outcome of a single file: either symbols and
// relations, or a non-nil Error when the file failed to parse.
type FileResult struct {
	File      string         `json:"file"`
	Symbols   []CodeSymbol   `json:"symbols,omitempty"`
	Relations []CodeRelation `json:"relations,omitempty"`
	Error     *ParseError    `json:"error,omitempty"`
}

// ParseResult is the aggregate outcome of parsing a batch of files.
type ParseResult struct {
	Files []FileResult `json:"files"`
}

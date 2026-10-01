package codeparser

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// jsParser implements Parser for JavaScript and TypeScript source using
// tree-sitter (CGo native). One instance handles exactly one language; use
// NewJSTSParserForPath to pick the right parser by file extension.
type jsParser struct {
	lang     *tree_sitter.Language
	language Language
}

// NewJSParser returns a Parser for JavaScript source files (.js / .jsx).
func NewJSParser() Parser {
	return &jsParser{
		lang:     tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
		language: LanguageJavaScript,
	}
}

// NewTSParser returns a Parser for TypeScript source files (.ts / .tsx). It
// uses the TSX grammar, which accepts both plain TypeScript and JSX.
func NewTSParser() Parser {
	return newTSParser(tree_sitter_typescript.LanguageTSX())
}

// newTSParser builds a TypeScript parser on a specific grammar: the plain TS
// grammar for .ts (so angle-bracket type assertions are not misread as JSX)
// or the TSX grammar for .tsx.
func newTSParser(lang unsafe.Pointer) Parser {
	return &jsParser{
		lang:     tree_sitter.NewLanguage(lang),
		language: LanguageTypeScript,
	}
}

// NewJSTSParserForPath returns the JavaScript or TypeScript parser matching
// the file extension: .js/.jsx/.mjs/.cjs for JavaScript, .ts/.tsx for
// TypeScript. Any other extension is an error.
func NewJSTSParserForPath(path string) (Parser, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".mjs", ".cjs":
		return NewJSParser(), nil
	case ".ts":
		return newTSParser(tree_sitter_typescript.LanguageTypescript()), nil
	case ".tsx":
		return newTSParser(tree_sitter_typescript.LanguageTSX()), nil
	default:
		return nil, fmt.Errorf("codeparser: %s: unsupported JS/TS extension", path)
	}
}

// Language returns the language this parser handles.
func (p *jsParser) Language() Language { return p.language }

// ParseFile parses one JS/TS source file into symbols and relations. CALLS
// and REFERENCES are resolved by name within the single file only; no
// cross-file or type-aware resolution is performed.
func (p *jsParser) ParseFile(path string, src []byte) (*FileResult, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("set language: %w", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter returned nil tree")
	}
	defer tree.Close()

	root := tree.RootNode()
	if root.HasError() {
		return nil, &ParseError{File: path, Message: "source contains syntax errors"}
	}

	e := &jsExtractor{
		path:      path,
		src:       src,
		language:  p.language,
		symbols:   []CodeSymbol{},
		relations: []CodeRelation{},
		funcs:     map[string]string{},
		vars:      map[string]string{},
		types:     map[string]string{},
	}
	e.extract(root)

	return &FileResult{
		File:      path,
		Symbols:   e.symbols,
		Relations: e.relations,
	}, nil
}

// jsExtractor walks a JS/TS syntax tree and accumulates symbols and
// relations. The grammar is a shared superset: TypeScript-only node kinds
// (interface_declaration, implements_clause, ...) never appear in JavaScript
// trees, so one extractor serves both languages.
type jsExtractor struct {
	path      string
	src       []byte
	language  Language
	symbols   []CodeSymbol
	relations []CodeRelation

	moduleID string

	// name -> symbol ID indexes for single-file, name-based resolution.
	funcs map[string]string // function/method name -> symbol ID
	vars  map[string]string // module-level variable name -> symbol ID
	types map[string]string // class/interface name -> symbol ID
}

func (e *jsExtractor) nodeRange(n *tree_sitter.Node) Range {
	sp, ep := n.StartPosition(), n.EndPosition()
	return Range{
		Start: Position{Line: uint32(sp.Row), Column: uint32(sp.Column)},
		End:   Position{Line: uint32(ep.Row), Column: uint32(ep.Column)},
	}
}

func (e *jsExtractor) text(n *tree_sitter.Node) string {
	return n.Utf8Text(e.src)
}

func (e *jsExtractor) addSymbol(s CodeSymbol) {
	e.symbols = append(e.symbols, s)
}

func (e *jsExtractor) addRelation(from, to string, t RelationType) {
	e.relations = append(e.relations, CodeRelation{From: from, To: to, Type: t})
}

func (e *jsExtractor) extract(root *tree_sitter.Node) {
	// File symbol.
	fileID := e.path
	e.addSymbol(CodeSymbol{
		ID:       fileID,
		Kind:     KindFile,
		Name:     e.path,
		File:     e.path,
		Range:    e.nodeRange(root),
		Language: e.language,
	})

	// Module symbol: a JS/TS file is an ES module.
	e.moduleID = e.path + ":module"
	e.addSymbol(CodeSymbol{
		ID:       e.moduleID,
		Kind:     KindModule,
		Name:     e.path,
		File:     e.path,
		Range:    e.nodeRange(root),
		Language: e.language,
	})
	e.addRelation(fileID, e.moduleID, RelationContains)

	cursor := root.Walk()
	defer cursor.Close()
	for _, child := range root.NamedChildren(cursor) {
		e.extractTopLevel(&child)
	}
}

// extractTopLevel dispatches one top-level statement. export_statement is
// unwrapped so exported declarations produce the same symbols as unexported
// ones.
func (e *jsExtractor) extractTopLevel(n *tree_sitter.Node) {
	switch n.Kind() {
	case "import_statement":
		e.extractImport(n)
	case "export_statement":
		if decl := n.ChildByFieldName("declaration"); decl != nil {
			e.extractTopLevel(decl)
		}
	case "function_declaration", "generator_function_declaration":
		e.extractFunction(n)
	case "class_declaration", "abstract_class_declaration":
		e.extractClass(n)
	case "interface_declaration":
		e.extractInterface(n)
	case "lexical_declaration", "variable_declaration":
		e.extractVar(n)
	}
}

func (e *jsExtractor) extractImport(n *tree_sitter.Node) {
	srcNode := n.ChildByFieldName("source")
	if srcNode == nil {
		return
	}
	importPath := strings.Trim(e.text(srcNode), `"'`)
	impID := e.path + ":import." + importPath
	e.addSymbol(CodeSymbol{
		ID:       impID,
		Kind:     KindImport,
		Name:     importPath,
		File:     e.path,
		Range:    e.nodeRange(n),
		Language: e.language,
		Attributes: map[string]string{
			"path": importPath,
		},
	})
	e.addRelation(e.moduleID, impID, RelationImports)

	// Imported bindings are deliberately NOT indexed into e.funcs: an imported
	// name has no emitted symbol in this file, so a CALLS edge to it would
	// dangle (its To endpoint unresolvable by the downstream graph store).
	// This matches the Go parser, which only emits the Import symbol.
}

func (e *jsExtractor) extractFunction(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := e.moduleID + "." + name
	e.addSymbol(CodeSymbol{
		ID:        id,
		Kind:      KindFunction,
		Name:      name,
		File:      e.path,
		Range:     e.nodeRange(n),
		Signature: e.signature(n),
		Language:  e.language,
	})
	e.addRelation(e.moduleID, id, RelationContains)
	e.funcs[name] = id

	if body := n.ChildByFieldName("body"); body != nil {
		e.extractBodyRefs(body, id)
	}
}

func (e *jsExtractor) extractClass(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := e.moduleID + "." + name
	e.addSymbol(CodeSymbol{
		ID:       id,
		Kind:     KindClass,
		Name:     name,
		File:     e.path,
		Range:    e.nodeRange(n),
		Language: e.language,
	})
	e.addRelation(e.moduleID, id, RelationContains)
	e.types[name] = id

	// Heritage: class_heritage (JS: expression child) or extends_clause /
	// implements_clause (TS).
	if heritage := findChildByKind(n, "class_heritage"); heritage != nil {
		e.extractHeritage(heritage, id)
	}

	if body := n.ChildByFieldName("body"); body != nil {
		e.extractClassBody(body, id)
	}
}

// extractHeritage records INHERITS relations from a class_heritage node to
// every named superclass / superinterface.
func (e *jsExtractor) extractHeritage(heritage *tree_sitter.Node, ownerID string) {
	cursor := heritage.Walk()
	defer cursor.Close()
	for _, child := range heritage.NamedChildren(cursor) {
		switch child.Kind() {
		case "extends_clause", "implements_clause":
			e.extractHeritage(&child, ownerID)
		default:
			if name := heritageTargetName(&child, e.src); name != "" {
				if target, ok := e.types[name]; ok {
					e.addRelation(ownerID, target, RelationInherits)
				}
			}
		}
	}
}

// heritageTargetName extracts the simple type name from a heritage entry:
// identifier / type_identifier -> itself, member_expression -> its property,
// generic_type -> its name.
func heritageTargetName(n *tree_sitter.Node, src []byte) string {
	switch n.Kind() {
	case "identifier", "type_identifier":
		return n.Utf8Text(src)
	case "member_expression":
		if prop := n.ChildByFieldName("property"); prop != nil {
			return prop.Utf8Text(src)
		}
	case "generic_type":
		if name := n.ChildByFieldName("name"); name != nil {
			return heritageTargetName(name, src)
		}
	}
	return ""
}

// extractClassBody extracts methods from a class body. Both grammars nest
// members directly under class_body (JS also exposes them via a "member"
// field; TS does not, so kind matching is used uniformly).
func (e *jsExtractor) extractClassBody(body *tree_sitter.Node, classID string) {
	cursor := body.Walk()
	defer cursor.Close()
	for _, child := range body.NamedChildren(cursor) {
		if child.Kind() == "method_definition" {
			e.extractMethod(&child, classID)
		}
	}
}

func (e *jsExtractor) extractMethod(n *tree_sitter.Node, classID string) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := classID + "." + name
	e.addSymbol(CodeSymbol{
		ID:        id,
		Kind:      KindMethod,
		Name:      name,
		File:      e.path,
		Range:     e.nodeRange(n),
		Signature: e.signature(n),
		Language:  e.language,
	})
	e.addRelation(classID, id, RelationContains)
	e.funcs[name] = id

	if body := n.ChildByFieldName("body"); body != nil {
		e.extractBodyRefs(body, id)
	}
}

func (e *jsExtractor) extractInterface(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := e.moduleID + "." + name
	e.addSymbol(CodeSymbol{
		ID:       id,
		Kind:     KindClass, // interface: closest language-agnostic kind
		Name:     name,
		File:     e.path,
		Range:    e.nodeRange(n),
		Language: e.language,
	})
	e.addRelation(e.moduleID, id, RelationContains)
	e.types[name] = id

	// extends_type_clause -> INHERITS.
	for _, ext := range findDescendantsByKind(n, "extends_type_clause") {
		e.extractHeritage(&ext, id)
	}

	// Method signatures.
	if body := n.ChildByFieldName("body"); body != nil {
		cursor := body.Walk()
		defer cursor.Close()
		for _, child := range body.NamedChildren(cursor) {
			if child.Kind() == "method_signature" || child.Kind() == "abstract_method_signature" {
				e.extractMethodSignature(&child, id)
			}
		}
	}
}

func (e *jsExtractor) extractMethodSignature(n *tree_sitter.Node, ownerID string) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := ownerID + "." + name
	e.addSymbol(CodeSymbol{
		ID:        id,
		Kind:      KindMethod,
		Name:      name,
		File:      e.path,
		Range:     e.nodeRange(n),
		Signature: strings.TrimSpace(e.text(n)),
		Language:  e.language,
	})
	e.addRelation(ownerID, id, RelationContains)
	e.funcs[name] = id
}

func (e *jsExtractor) extractVar(n *tree_sitter.Node) {
	for _, decl := range findDescendantsByKind(n, "variable_declarator") {
		nameNode := decl.ChildByFieldName("name")
		if nameNode == nil || nameNode.Kind() != "identifier" {
			continue // destructuring patterns are not single-name symbols
		}
		name := e.text(nameNode)
		id := e.moduleID + "." + name
		attrs := map[string]string{}
		if value := decl.ChildByFieldName("value"); value != nil {
			attrs["value"] = value.Kind()
		}
		e.addSymbol(CodeSymbol{
			ID:         id,
			Kind:       KindVariable,
			Name:       name,
			File:       e.path,
			Range:      e.nodeRange(&decl),
			Language:   e.language,
			Attributes: attrs,
		})
		e.addRelation(e.moduleID, id, RelationContains)
		e.vars[name] = id

		if value := decl.ChildByFieldName("value"); value != nil {
			e.extractBodyRefs(value, id)
		}
	}
}

// extractBodyRefs walks a function/method body (or a variable initializer)
// and records CALLS and REFERENCES relations resolved by name within this
// file.
func (e *jsExtractor) extractBodyRefs(body *tree_sitter.Node, callerID string) {
	seenCalls := map[string]bool{}
	seenRefs := map[string]bool{}

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				// Known limitation: a method called through a receiver
				// (x.F()) resolves by bare name only, so it can hit a
				// same-named function. Acceptable at single-file,
				// syntax-level depth (no type checking).
				name := jsCalleeName(fn, e.src)
				if target, ok := e.funcs[name]; ok && target != callerID && !seenCalls[target] {
					seenCalls[target] = true
					e.addRelation(callerID, target, RelationCalls)
				}
			}
		case "identifier":
			// A bare identifier that names a module-level variable is a
			// reference, unless it is part of a call/member expression.
			//
			// Known limitation: a local variable that shadows a module-level
			// name is still counted as a reference to the module-level one.
			// Acceptable at single-file, syntax-level depth (no scope
			// analysis).
			if target, ok := e.vars[e.text(n)]; ok && !seenRefs[target] {
				if !isCallOrMemberPart(n) {
					seenRefs[target] = true
					e.addRelation(callerID, target, RelationReferences)
				}
			}
		}
		cursor := n.Walk()
		defer cursor.Close()
		for _, child := range n.NamedChildren(cursor) {
			walk(&child)
		}
	}
	walk(body)
}

// signature returns the declaration text of a function/method without its
// body.
func (e *jsExtractor) signature(n *tree_sitter.Node) string {
	sig := e.text(n)
	if body := n.ChildByFieldName("body"); body != nil {
		bodyText := e.text(body)
		if idx := strings.LastIndex(sig, bodyText); idx > 0 {
			sig = strings.TrimSpace(sig[:idx])
		}
	}
	return sig
}

// jsCalleeName extracts the simple name being called from a call target:
// identifier -> itself, member_expression -> its property.
func jsCalleeName(fn *tree_sitter.Node, src []byte) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Utf8Text(src)
	case "member_expression":
		if prop := fn.ChildByFieldName("property"); prop != nil {
			return prop.Utf8Text(src)
		}
	}
	return ""
}

// isCallOrMemberPart reports whether n is the function name of a call or
// part of a member expression (i.e. not a standalone use).
func isCallOrMemberPart(n *tree_sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case "call_expression":
		if fn := parent.ChildByFieldName("function"); fn != nil && fn.Equals(*n) {
			return true
		}
	case "member_expression":
		return true
	}
	return false
}

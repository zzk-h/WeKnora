package codeparser

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// goParser implements Parser for Go source using tree-sitter (CGo native).
type goParser struct {
	lang *tree_sitter.Language
}

// NewGoParser returns a Parser for Go source files.
func NewGoParser() Parser {
	return &goParser{lang: tree_sitter.NewLanguage(tree_sitter_go.Language())}
}

// Language returns LanguageGo.
func (p *goParser) Language() Language { return LanguageGo }

// ParseFile parses one Go source file into symbols and relations. CALLS and
// REFERENCES are resolved by name within the single file only; no cross-file
// or type-aware resolution is performed.
func (p *goParser) ParseFile(path string, src []byte) (*FileResult, error) {
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

	g := &goExtractor{
		path:      path,
		src:       src,
		symbols:   []CodeSymbol{},
		relations: []CodeRelation{},
		funcs:     map[string]string{},
		vars:      map[string]string{},
		types:     map[string]string{},
	}
	g.extract(root)

	return &FileResult{
		File:      path,
		Symbols:   g.symbols,
		Relations: g.relations,
	}, nil
}

// goExtractor walks a Go syntax tree and accumulates symbols and relations.
type goExtractor struct {
	path      string
	src       []byte
	symbols   []CodeSymbol
	relations []CodeRelation

	pkgName string
	pkgID   string

	// name -> symbol ID indexes for single-file, name-based resolution.
	funcs map[string]string // function/method name -> symbol ID
	vars  map[string]string // package-level variable name -> symbol ID
	types map[string]string // type name -> symbol ID
}

func (g *goExtractor) nodeRange(n *tree_sitter.Node) Range {
	sp, ep := n.StartPosition(), n.EndPosition()
	return Range{
		Start: Position{Line: uint32(sp.Row), Column: uint32(sp.Column)},
		End:   Position{Line: uint32(ep.Row), Column: uint32(ep.Column)},
	}
}

func (g *goExtractor) text(n *tree_sitter.Node) string {
	return n.Utf8Text(g.src)
}

func (g *goExtractor) addSymbol(s CodeSymbol) {
	g.symbols = append(g.symbols, s)
}

func (g *goExtractor) addRelation(from, to string, t RelationType) {
	g.relations = append(g.relations, CodeRelation{From: from, To: to, Type: t})
}

func (g *goExtractor) extract(root *tree_sitter.Node) {
	// File symbol.
	fileID := g.path
	g.addSymbol(CodeSymbol{
		ID:       fileID,
		Kind:     KindFile,
		Name:     g.path,
		File:     g.path,
		Range:    g.nodeRange(root),
		Language: LanguageGo,
	})

	// Package clause.
	pkgClause := findChildByKind(root, "package_clause")
	if pkgClause == nil {
		return
	}
	pkgIdent := pkgClause.ChildByFieldName("name")
	if pkgIdent == nil {
		pkgIdent = findChildByKind(pkgClause, "package_identifier")
	}
	if pkgIdent == nil {
		return
	}
	g.pkgName = g.text(pkgIdent)
	g.pkgID = g.path + ":" + g.pkgName
	g.addSymbol(CodeSymbol{
		ID:       g.pkgID,
		Kind:     KindPackage,
		Name:     g.pkgName,
		File:     g.path,
		Range:    g.nodeRange(pkgClause),
		Language: LanguageGo,
	})
	g.addRelation(fileID, g.pkgID, RelationContains)

	cursor := root.Walk()
	defer cursor.Close()
	for _, child := range root.NamedChildren(cursor) {
		switch child.Kind() {
		case "import_declaration":
			g.extractImport(&child)
		case "function_declaration":
			g.extractFunction(&child)
		case "method_declaration":
			g.extractMethod(&child)
		case "type_declaration":
			g.extractType(&child)
		case "var_declaration", "const_declaration":
			g.extractVar(&child)
		}
	}
}

func (g *goExtractor) extractImport(n *tree_sitter.Node) {
	specs := findDescendantsByKind(n, "import_spec")
	for i := range specs {
		spec := &specs[i]
		pathNode := spec.ChildByFieldName("path")
		if pathNode == nil {
			continue
		}
		importPath := strings.Trim(g.text(pathNode), `"`)
		impID := g.path + ":import." + importPath
		g.addSymbol(CodeSymbol{
			ID:       impID,
			Kind:     KindImport,
			Name:     importPath,
			File:     g.path,
			Range:    g.nodeRange(spec),
			Language: LanguageGo,
			Attributes: map[string]string{
				"path": importPath,
			},
		})
		g.addRelation(g.pkgID, impID, RelationImports)
	}
}

func (g *goExtractor) extractFunction(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := g.text(nameNode)
	id := g.pkgID + "." + name
	g.addSymbol(CodeSymbol{
		ID:        id,
		Kind:      KindFunction,
		Name:      name,
		File:      g.path,
		Range:     g.nodeRange(n),
		Signature: g.signature(n),
		Language:  LanguageGo,
	})
	g.addRelation(g.pkgID, id, RelationContains)
	g.funcs[name] = id

	if body := n.ChildByFieldName("body"); body != nil {
		g.extractBodyRefs(body, id)
	}
}

func (g *goExtractor) extractMethod(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := g.text(nameNode)

	recvNode := n.ChildByFieldName("receiver")
	recvType := ""
	recvName := ""
	if recvNode != nil {
		recvType = receiverTypeText(g.text(recvNode))
		recvName = strings.TrimPrefix(recvType, "*")
	}

	id := g.pkgID + "." + recvName + "." + name
	attrs := map[string]string{}
	if recvType != "" {
		attrs["receiver"] = recvType
	}
	g.addSymbol(CodeSymbol{
		ID:         id,
		Kind:       KindMethod,
		Name:       name,
		File:       g.path,
		Range:      g.nodeRange(n),
		Signature:  g.signature(n),
		Language:   LanguageGo,
		Attributes: attrs,
	})
	g.addRelation(g.pkgID, id, RelationContains)
	if recvName != "" {
		if typeID, ok := g.types[recvName]; ok {
			g.addRelation(typeID, id, RelationContains)
		}
	}
	g.funcs[name] = id

	if body := n.ChildByFieldName("body"); body != nil {
		g.extractBodyRefs(body, id)
	}
}

func (g *goExtractor) extractType(n *tree_sitter.Node) {
	specs := findDescendantsByKind(n, "type_spec")
	for i := range specs {
		spec := &specs[i]
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := g.text(nameNode)
		typeNode := spec.ChildByFieldName("type")

		kind := KindStruct
		if typeNode != nil && typeNode.Kind() == "interface_type" {
			kind = KindClass // interface: closest language-agnostic kind
		}

		id := g.pkgID + "." + name
		g.addSymbol(CodeSymbol{
			ID:       id,
			Kind:     kind,
			Name:     name,
			File:     g.path,
			Range:    g.nodeRange(spec),
			Language: LanguageGo,
		})
		g.addRelation(g.pkgID, id, RelationContains)
		g.types[name] = id

		// Embedded types -> INHERITS.
		if typeNode != nil && typeNode.Kind() == "struct_type" {
			g.extractEmbedded(typeNode, id)
		}
	}
}

func (g *goExtractor) extractEmbedded(structType *tree_sitter.Node, ownerID string) {
	fields := findDescendantsByKind(structType, "field_declaration")
	for i := range fields {
		fd := &fields[i]
		// An embedded field has a type but no name.
		if fd.ChildByFieldName("name") != nil {
			continue
		}
		typeNode := fd.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		embedded := strings.TrimPrefix(g.text(typeNode), "*")
		if target, ok := g.types[embedded]; ok {
			g.addRelation(ownerID, target, RelationInherits)
		}
	}
}

func (g *goExtractor) extractVar(n *tree_sitter.Node) {
	specKind := "var_spec"
	if n.Kind() == "const_declaration" {
		specKind = "const_spec"
	}
	specs := findDescendantsByKind(n, specKind)
	for i := range specs {
		spec := &specs[i]
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := g.text(nameNode)
		id := g.pkgID + "." + name
		g.addSymbol(CodeSymbol{
			ID:       id,
			Kind:     KindVariable,
			Name:     name,
			File:     g.path,
			Range:    g.nodeRange(spec),
			Language: LanguageGo,
		})
		g.addRelation(g.pkgID, id, RelationContains)
		g.vars[name] = id
	}
}

// extractBodyRefs walks a function/method body and records CALLS and
// REFERENCES relations resolved by name within this file.
func (g *goExtractor) extractBodyRefs(body *tree_sitter.Node, callerID string) {
	seenCalls := map[string]bool{}
	seenRefs := map[string]bool{}

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				// Known limitation: a method value called through a receiver
				// (x.F()) resolves by bare name only, so it can hit a
				// same-named function. Acceptable at single-file,
				// syntax-level depth (no type checking).
				name := calleeName(fn, g.src)
				if target, ok := g.funcs[name]; ok && target != callerID && !seenCalls[target] {
					seenCalls[target] = true
					g.addRelation(callerID, target, RelationCalls)
				}
			}
		case "identifier":
			// A bare identifier that names a package-level variable is a
			// reference, unless it is part of a call/selector expression.
			//
			// Known limitation: a local variable that shadows a package-level
			// name is still counted as a reference to the package-level one.
			// Acceptable at single-file, syntax-level depth (no scope analysis).
			if target, ok := g.vars[g.text(n)]; ok && !seenRefs[target] {
				if !isCallOrSelectorPart(n) {
					seenRefs[target] = true
					g.addRelation(callerID, target, RelationReferences)
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

// signature returns the declaration text of a function/method without its body.
func (g *goExtractor) signature(n *tree_sitter.Node) string {
	sig := g.text(n)
	if body := n.ChildByFieldName("body"); body != nil {
		bodyText := g.text(body)
		if idx := strings.LastIndex(sig, bodyText); idx > 0 {
			sig = strings.TrimSpace(sig[:idx])
		}
	}
	return sig
}

// calleeName extracts the simple name being called from a call target:
// identifier -> itself, selector_expression -> its field.
func calleeName(fn *tree_sitter.Node, src []byte) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Utf8Text(src)
	case "selector_expression":
		if field := fn.ChildByFieldName("field"); field != nil {
			return field.Utf8Text(src)
		}
	}
	return ""
}

// isCallOrSelectorPart reports whether n is the function name of a call or
// the operand/field of a selector expression (i.e. not a standalone use).
func isCallOrSelectorPart(n *tree_sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case "call_expression":
		if fn := parent.ChildByFieldName("function"); fn != nil && fn.Equals(*n) {
			return true
		}
	case "selector_expression":
		return true
	}
	return false
}

// receiverTypeText extracts the type part from a receiver parameter list,
// e.g. "(p *Person)" -> "*Person".
func receiverTypeText(recv string) string {
	recv = strings.TrimSpace(recv)
	recv = strings.TrimPrefix(recv, "(")
	recv = strings.TrimSuffix(recv, ")")
	fields := strings.Fields(recv)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// findChildByKind returns the first direct child with the given kind.
func findChildByKind(n *tree_sitter.Node, kind string) *tree_sitter.Node {
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.NamedChildren(cursor) {
		if child.Kind() == kind {
			return &child
		}
	}
	return nil
}

// findDescendantsByKind returns all descendants (including direct children)
// with the given kind, in document order.
func findDescendantsByKind(n *tree_sitter.Node, kind string) []tree_sitter.Node {
	var out []tree_sitter.Node
	var walk func(cur *tree_sitter.Node)
	walk = func(cur *tree_sitter.Node) {
		if cur.Kind() == kind {
			out = append(out, *cur)
			return
		}
		cursor := cur.Walk()
		defer cursor.Close()
		for _, child := range cur.NamedChildren(cursor) {
			walk(&child)
		}
	}
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.NamedChildren(cursor) {
		walk(&child)
	}
	return out
}

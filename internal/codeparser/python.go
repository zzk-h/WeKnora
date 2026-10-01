package codeparser

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// pythonParser implements Parser for Python source using tree-sitter (CGo
// native).
type pythonParser struct {
	lang *tree_sitter.Language
}

// NewPythonParser returns a Parser for Python source files.
func NewPythonParser() Parser {
	return &pythonParser{lang: tree_sitter.NewLanguage(tree_sitter_python.Language())}
}

// Language returns LanguagePython.
func (p *pythonParser) Language() Language { return LanguagePython }

// ParseFile parses one Python source file into symbols and relations. CALLS
// and REFERENCES are resolved by name within the single file only; no
// cross-file or type-aware resolution is performed.
func (p *pythonParser) ParseFile(path string, src []byte) (*FileResult, error) {
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

	e := &pyExtractor{
		path:      path,
		src:       src,
		symbols:   []CodeSymbol{},
		relations: []CodeRelation{},
		funcs:     map[string]string{},
		vars:      map[string]string{},
		classes:   map[string]string{},
	}
	e.extract(root)

	return &FileResult{
		File:      path,
		Symbols:   e.symbols,
		Relations: e.relations,
	}, nil
}

// pyExtractor walks a Python syntax tree and accumulates symbols and
// relations.
type pyExtractor struct {
	path      string
	src       []byte
	symbols   []CodeSymbol
	relations []CodeRelation

	modName string
	modID   string

	// name -> symbol ID indexes for single-file, name-based resolution.
	funcs   map[string]string // function/method name -> symbol ID
	vars    map[string]string // module-level variable name -> symbol ID
	classes map[string]string // class name -> symbol ID
}

func (e *pyExtractor) nodeRange(n *tree_sitter.Node) Range {
	sp, ep := n.StartPosition(), n.EndPosition()
	return Range{
		Start: Position{Line: uint32(sp.Row), Column: uint32(sp.Column)},
		End:   Position{Line: uint32(ep.Row), Column: uint32(ep.Column)},
	}
}

func (e *pyExtractor) text(n *tree_sitter.Node) string {
	return n.Utf8Text(e.src)
}

func (e *pyExtractor) addSymbol(s CodeSymbol) {
	e.symbols = append(e.symbols, s)
}

func (e *pyExtractor) addRelation(from, to string, t RelationType) {
	e.relations = append(e.relations, CodeRelation{From: from, To: to, Type: t})
}

// moduleName derives the module name from the file path's base name without
// the .py extension.
func moduleName(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".py")
}

func (e *pyExtractor) extract(root *tree_sitter.Node) {
	// File symbol.
	fileID := e.path
	e.addSymbol(CodeSymbol{
		ID:       fileID,
		Kind:     KindFile,
		Name:     e.path,
		File:     e.path,
		Range:    e.nodeRange(root),
		Language: LanguagePython,
	})

	// Module symbol: a Python file is a module.
	e.modName = moduleName(e.path)
	e.modID = e.path + ":" + e.modName
	e.addSymbol(CodeSymbol{
		ID:       e.modID,
		Kind:     KindModule,
		Name:     e.modName,
		File:     e.path,
		Range:    e.nodeRange(root),
		Language: LanguagePython,
	})
	e.addRelation(fileID, e.modID, RelationContains)

	cursor := root.Walk()
	defer cursor.Close()
	for _, child := range root.NamedChildren(cursor) {
		switch child.Kind() {
		case "import_statement", "import_from_statement":
			e.extractImport(&child)
		case "function_definition":
			e.extractFunction(&child, e.modID, "")
		case "class_definition":
			e.extractClass(&child)
		case "expression_statement":
			e.extractModuleVar(&child)
		}
	}
}

func (e *pyExtractor) extractImport(n *tree_sitter.Node) {
	switch n.Kind() {
	case "import_statement":
		// import os / import os as o / import a, b
		e.extractImportNames(n)
	case "import_from_statement":
		// from collections import OrderedDict -> import the module "collections"
		if mod := n.ChildByFieldName("module_name"); mod != nil {
			e.addImport(e.text(mod), n)
		}
	}
}

// extractImportNames handles "import a" / "import a as b" / "import a, b".
func (e *pyExtractor) extractImportNames(n *tree_sitter.Node) {
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.NamedChildren(cursor) {
		switch child.Kind() {
		case "dotted_name":
			e.addImport(e.text(&child), &child)
		case "aliased_import":
			if name := child.ChildByFieldName("name"); name != nil {
				e.addImport(e.text(name), &child)
			}
		}
	}
}

func (e *pyExtractor) addImport(path string, n *tree_sitter.Node) {
	impID := e.path + ":import." + path
	e.addSymbol(CodeSymbol{
		ID:       impID,
		Kind:     KindImport,
		Name:     path,
		File:     e.path,
		Range:    e.nodeRange(n),
		Language: LanguagePython,
		Attributes: map[string]string{
			"path": path,
		},
	})
	e.addRelation(e.modID, impID, RelationImports)
}

// extractFunction handles a function definition. When className is empty the
// symbol is a module-level Function contained by the module; otherwise it is
// a Method contained by the class.
func (e *pyExtractor) extractFunction(n *tree_sitter.Node, parentID, className string) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)

	kind := KindFunction
	id := e.modID + "." + name
	if className != "" {
		kind = KindMethod
		id = e.modID + "." + className + "." + name
	}
	e.addSymbol(CodeSymbol{
		ID:        id,
		Kind:      kind,
		Name:      name,
		File:      e.path,
		Range:     e.nodeRange(n),
		Signature: e.signature(n),
		Language:  LanguagePython,
	})
	e.addRelation(parentID, id, RelationContains)
	e.funcs[name] = id

	if body := n.ChildByFieldName("body"); body != nil {
		e.extractBodyRefs(body, id)
	}
}

func (e *pyExtractor) extractClass(n *tree_sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := e.text(nameNode)
	id := e.modID + "." + name
	e.addSymbol(CodeSymbol{
		ID:       id,
		Kind:     KindClass,
		Name:     name,
		File:     e.path,
		Range:    e.nodeRange(n),
		Language: LanguagePython,
	})
	e.addRelation(e.modID, id, RelationContains)
	e.classes[name] = id

	// Superclasses -> INHERITS.
	if sup := n.ChildByFieldName("superclasses"); sup != nil {
		e.extractSuperclasses(sup, id)
	}

	// Methods inside the class body.
	if body := n.ChildByFieldName("body"); body != nil {
		cursor := body.Walk()
		defer cursor.Close()
		for _, child := range body.NamedChildren(cursor) {
			if child.Kind() == "function_definition" {
				e.extractFunction(&child, id, name)
			}
		}
	}
}

// extractSuperclasses records INHERITS relations for each superclass named in
// the argument list, resolved by name within this file.
func (e *pyExtractor) extractSuperclasses(sup *tree_sitter.Node, ownerID string) {
	cursor := sup.Walk()
	defer cursor.Close()
	for _, child := range sup.NamedChildren(cursor) {
		var baseName string
		switch child.Kind() {
		case "identifier":
			baseName = e.text(&child)
		case "attribute":
			// e.g. class Foo(mod.Base): resolve by the attribute name.
			if attr := child.ChildByFieldName("attribute"); attr != nil {
				baseName = e.text(attr)
			}
		}
		if baseName == "" {
			continue
		}
		if target, ok := e.classes[baseName]; ok {
			e.addRelation(ownerID, target, RelationInherits)
		}
	}
}

// extractModuleVar records module-level assignments as Variable symbols.
func (e *pyExtractor) extractModuleVar(n *tree_sitter.Node) {
	cursor := n.Walk()
	defer cursor.Close()
	for _, child := range n.NamedChildren(cursor) {
		if child.Kind() != "assignment" {
			continue
		}
		left := child.ChildByFieldName("left")
		if left == nil || left.Kind() != "identifier" {
			continue
		}
		name := e.text(left)
		id := e.modID + "." + name
		e.addSymbol(CodeSymbol{
			ID:       id,
			Kind:     KindVariable,
			Name:     name,
			File:     e.path,
			Range:    e.nodeRange(&child),
			Language: LanguagePython,
		})
		e.addRelation(e.modID, id, RelationContains)
		e.vars[name] = id
	}
}

// extractBodyRefs walks a function/method body and records CALLS and
// REFERENCES relations resolved by name within this file.
func (e *pyExtractor) extractBodyRefs(body *tree_sitter.Node, callerID string) {
	seenCalls := map[string]bool{}
	seenRefs := map[string]bool{}

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "call":
			fn := n.ChildByFieldName("function")
			if fn != nil {
				// Known limitation: a method called through an object
				// (x.F()) resolves by bare attribute name only, so it can
				// hit a same-named function. Acceptable at single-file,
				// syntax-level depth (no type checking).
				name := pyCalleeName(fn, e.src)
				if target, ok := e.funcs[name]; ok && target != callerID && !seenCalls[target] {
					seenCalls[target] = true
					e.addRelation(callerID, target, RelationCalls)
				}
			}
		case "identifier":
			// A bare identifier that names a module-level variable is a
			// reference, unless it is part of a call/attribute expression.
			//
			// Known limitation: a local variable that shadows a module-level
			// name is still counted as a reference to the module-level one.
			// Acceptable at single-file, syntax-level depth (no scope
			// analysis).
			if target, ok := e.vars[e.text(n)]; ok && !seenRefs[target] {
				if !isPyCallOrAttributePart(n) {
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
func (e *pyExtractor) signature(n *tree_sitter.Node) string {
	sig := e.text(n)
	if body := n.ChildByFieldName("body"); body != nil {
		bodyText := e.text(body)
		if idx := strings.LastIndex(sig, bodyText); idx > 0 {
			sig = strings.TrimSpace(sig[:idx])
			sig = strings.TrimSuffix(sig, ":")
		}
	}
	return sig
}

// pyCalleeName extracts the simple name being called from a call target:
// identifier -> itself, attribute -> its attribute name.
func pyCalleeName(fn *tree_sitter.Node, src []byte) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Utf8Text(src)
	case "attribute":
		if attr := fn.ChildByFieldName("attribute"); attr != nil {
			return attr.Utf8Text(src)
		}
	}
	return ""
}

// isPyCallOrAttributePart reports whether n is the function name of a call or
// the object/attribute of an attribute expression (i.e. not a standalone
// use).
func isPyCallOrAttributePart(n *tree_sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case "call":
		if fn := parent.ChildByFieldName("function"); fn != nil && fn.Equals(*n) {
			return true
		}
	case "attribute":
		return true
	}
	return false
}

package codeparser

// SourceFile is one unit of parse input: a path plus its source bytes.
type SourceFile struct {
	Path   string
	Source []byte
}

// Parser parses source files of one language into symbols and relations.
// Implementations must be safe for sequential use; concurrent use requires
// one Parser instance per goroutine.
type Parser interface {
	// Language returns the language this parser handles.
	Language() Language
	// ParseFile parses a single source file. A syntax-level failure is
	// reported as an error; callers decide how to degrade.
	ParseFile(path string, src []byte) (*FileResult, error)
}

// ParseBatch parses files with p and aggregates the per-file outcomes. A
// single file's failure degrades to that file's error result and does not
// abort the rest of the batch.
func ParseBatch(p Parser, files []SourceFile) *ParseResult {
	res := &ParseResult{Files: make([]FileResult, 0, len(files))}
	for _, f := range files {
		fr, err := p.ParseFile(f.Path, f.Source)
		if err != nil {
			res.Files = append(res.Files, FileResult{
				File:  f.Path,
				Error: &ParseError{File: f.Path, Message: err.Error()},
			})
			continue
		}
		if fr == nil {
			res.Files = append(res.Files, FileResult{
				File:  f.Path,
				Error: &ParseError{File: f.Path, Message: "parser returned nil result"},
			})
			continue
		}
		res.Files = append(res.Files, *fr)
	}
	return res
}

package goexec

import (
	"fmt"
	"github.com/pkg/errors"
	"io"
	"os"
	"sort"
	"strings"
)

// This file holds the various functions used to compose and render the go code that
// will be compiled, from the parsed cells.

// WriterWithCursor keep tabs of the current line/col of the file (presumably)
// being written.
type WriterWithCursor struct {
	w         io.Writer
	err       error // If err != nil, nothing is written anymore.
	Line, Col int
}

// NewWriterWithCursor that keeps tabs of current line/col of the file (presumably)
// being written.
func NewWriterWithCursor(w io.Writer) *WriterWithCursor {
	return &WriterWithCursor{w: w}
}

// Cursor returns the current position in the file, at the end of what has been written so far.
func (w *WriterWithCursor) Cursor() Cursor {
	return Cursor{Line: w.Line, Col: w.Col}
}

// CursorPlusDelta returns the expected cursor position in the current file, assuming the original cursor
// is cursorDelta away from the current position in the file (stored in w).
//
// Semantically it's equivalent to `w.Cursor() + cursorDelta`.
func (w *WriterWithCursor) CursorPlusDelta(delta Cursor) (fileCursor Cursor) {
	fileCursor = w.Cursor()
	fileCursor.Line += delta.Line
	if delta.Line > 0 {
		fileCursor.Col = delta.Col
	} else {
		fileCursor.Col += delta.Col
	}
	return fileCursor
}

// Error returns first error that happened during writing.
func (w *WriterWithCursor) Error() error { return w.err }

// Writef write with formatted text. Errors can be retrieved with Error.
func (w *WriterWithCursor) Writef(format string, args ...any) {
	if w.err != nil {
		return
	}
	text := fmt.Sprintf(format, args...)
	w.Write(text)
}

// Write writes the given content and keeps track of cursor. Errors can be retrieved with Error.
func (w *WriterWithCursor) Write(content string) {
	if w.err != nil {
		return
	}
	var n int
	n, w.err = w.w.Write([]byte(content))
	if n != len(content) {
		w.err = errors.Errorf("failed to write %q, %d bytes: wrote only %d", content, len(content), n)
	}
	if w.err != nil {
		return
	}

	// Update cursor position.
	parts := strings.Split(content, "\n")
	if len(parts) == 1 {
		w.Col += len(parts[0])
	} else {
		w.Line += len(parts) - 1
		w.Col = len(parts[len(parts)-1])
	}
}

func (s *State) writeLinesToFile(filePath string, lines <-chan string) (err error) {
	var f *os.File
	f, err = os.Create(filePath)
	if err != nil {
		return errors.Wrapf(err, "creating %q", filePath)
	}
	defer func() {
		newErr := f.Close()
		if newErr != nil && err == nil {
			err = errors.Wrapf(newErr, "closing %q", filePath)
		}
	}()
	for line := range lines {
		if err != nil {
			// If there was an error keep on reading to the end of channel, discarding the input.
			continue
		}
		_, err = fmt.Fprintf(f, "%s\n", line)
		if err != nil {
			err = errors.Wrapf(err, "writing to %q", filePath)
		}
	}
	return err
}

// sortedKeys enumerate keys and sort them.
func sortedKeys[T any](m map[string]T) (keys []string) {
	keys = make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return
}

// RenderImports writes out `import ( ... )` for all imports in Declarations.
func (d *Declarations) RenderImports(w *WriterWithCursor) (cursor Cursor) {
	cursor = NoCursor
	if len(d.Imports) == 0 {
		return
	}

	w.Write("import (\n")
	for _, key := range sortedKeys(d.Imports) {
		importDecl := d.Imports[key]
		w.Write("\t")
		if importDecl.Alias != "" {
			if importDecl.CursorInAlias {
				cursor = w.CursorPlusDelta(importDecl.Cursor)
			}
			w.Writef("%s ", importDecl.Alias)
		}
		if importDecl.CursorInPath {
			cursor = w.CursorPlusDelta(importDecl.Cursor)
		}
		w.Writef("%q\n", importDecl.Path)
	}
	w.Write(")\n\n")
	return
}

// RenderVariables writes out `var ( ... )` for all variables in Declarations.
func (d *Declarations) RenderVariables(w *WriterWithCursor) (cursor Cursor) {
	cursor = NoCursor
	if len(d.Variables) == 0 {
		return
	}

	w.Write("var (\n")
	for _, key := range sortedKeys(d.Variables) {
		varDecl := d.Variables[key]
		w.Write("\t")
		if varDecl.CursorInName {
			cursor = w.CursorPlusDelta(varDecl.Cursor)
		}
		w.Write(varDecl.Name)
		if varDecl.TypeDefinition != "" {
			w.Write(" ")
			if varDecl.CursorInType {
				cursor = w.CursorPlusDelta(varDecl.Cursor)
			}
			w.Write(varDecl.TypeDefinition)
		}
		if varDecl.ValueDefinition != "" {
			w.Write(" = ")
			if varDecl.CursorInValue {
				cursor = w.CursorPlusDelta(varDecl.Cursor)
			}
			w.Write(varDecl.ValueDefinition)
		}
		w.Write("\n")
	}
	w.Write(")\n\n")
	return
}

// RenderFunctions without comments, for all functions in Declarations.
func (d *Declarations) RenderFunctions(w *WriterWithCursor) (cursor Cursor) {
	cursor = NoCursor
	if len(d.Functions) == 0 {
		return
	}

	for _, key := range sortedKeys(d.Functions) {
		funcDecl := d.Functions[key]
		def := funcDecl.Definition
		if funcDecl.HasCursor() {
			cursor = w.CursorPlusDelta(funcDecl.Cursor)
		}
		if strings.HasPrefix(key, "init_") {
			// TODO: this will not work if there is a comment before the function
			//       which also has the string key. We need something more sophisticated.
			def = strings.Replace(def, key, "init", 1)
		}
		w.Writef("%s\n\n", def)
	}
	return
}

// RenderTypes without comments.
func (d *Declarations) RenderTypes(w *WriterWithCursor) (cursor Cursor) {
	cursor = NoCursor
	if len(d.Types) == 0 {
		return
	}

	for _, key := range sortedKeys(d.Types) {
		typeDecl := d.Types[key]
		w.Write("type ")
		if typeDecl.CursorInKey {
			cursor = w.CursorPlusDelta(typeDecl.Cursor)
		}
		w.Writef("%s ", key)
		if typeDecl.CursorInType {
			cursor = w.CursorPlusDelta(typeDecl.Cursor)
		}
		w.Writef("%s\n", typeDecl.TypeDefinition)
	}
	w.Write("\n")
	return
}

// RenderConstants without comments for all constants in Declarations.
//
// Constants are trickier to render because when they are defined in a block,
// using `iota`, their ordering matters. So we re-render them in the same order
// and blocks as they were originally parsed.
//
// The ordering is given by the sort order of the first element of each `const` block.
func (d *Declarations) RenderConstants(w *WriterWithCursor) (cursor Cursor) {
	cursor = NoCursor
	if len(d.Constants) == 0 {
		return
	}

	// Enumerate heads of const blocks.
	headKeys := make([]string, 0, len(d.Constants))
	for key, constDecl := range d.Constants {
		if constDecl.Prev == nil {
			// Head of the const block.
			headKeys = append(headKeys, key)
		}
	}
	sort.Strings(headKeys)

	for _, headKey := range headKeys {
		constDecl := d.Constants[headKey]
		if constDecl.Next == nil {
			// Render individual const declaration.
			w.Write("const ")
			constDecl.Render(w, &cursor)
			w.Write("\n\n")
			continue
		}
		// Render block of constants.
		w.Write("const (\n")
		for constDecl != nil {
			w.Write("\t")
			constDecl.Render(w, &cursor)
			w.Write("\n")
			constDecl = constDecl.Next
		}
		w.Write(")\n\n")
	}
	return
}

// Render Constant declaration (without the `const` keyword).
func (c *Constant) Render(w *WriterWithCursor, cursor *Cursor) {
	if c.CursorInKey {
		*cursor = w.CursorPlusDelta(c.Cursor)
	}
	w.Write(c.Key)
	if c.TypeDefinition != "" {
		w.Write(" ")
		if c.CursorInType {
			*cursor = w.CursorPlusDelta(c.Cursor)
		}
		w.Write(c.TypeDefinition)
	}
	if c.ValueDefinition != "" {
		w.Write(" = ")
		if c.CursorInValue {
			*cursor = w.CursorPlusDelta(c.Cursor)
		}
		w.Write(c.ValueDefinition)
	}
}

// createGoFileFromLines creates a main.go file from the cell contents. It doesn't yet include previous declarations.
//
// Among the things it handles:
// * Adding an initial `package main` line.
// * Handle the special `%%` line, a shortcut to create a `func main()`.
//
// Parameters:
// * filePath is the path where to write the Go code.
// * lines are the lines in the cell.
// * skipLines are lines in the cell that are not Go code: lines starting with "!" or "%" special characters.
// * cursorInCell optionally specifies the cursor position in the cell. It can be set to NoCursor.
//
// It returns cursorInFile, the equivalent cursor position in the final file, considering the given cursorInCell.
func (s *State) createGoFileFromLines(filePath string, lines []string, skipLines map[int]struct{}, cursorInCell Cursor) (cursorInFile Cursor, err error) {
	cursorInFile = NoCursor

	var f *os.File
	f, err = os.Create(filePath)
	if err != nil {
		return cursorInFile, errors.Wrapf(err, "Failed to create %q", filePath)
	}
	w := NewWriterWithCursor(f)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	w.Write("package main\n\n")
	var createdFuncMain bool
	for ii, line := range lines {
		if strings.HasPrefix(line, "%main") || strings.HasPrefix(line, "%%") {
			w.Write("func main() {\n\tflag.Parse\n")
			createdFuncMain = true
			continue
		}
		if _, found := skipLines[ii]; found {
			continue
		}
		if createdFuncMain && line != "" {
			w.Write("\t")
		}
		if ii == cursorInCell.Line {
			// Use current line for cursor, but add column.
			cursorInFile = w.CursorPlusDelta(Cursor{Col: cursorInCell.Col})
		}
		w.Write(line)
		w.Write("\n")
	}
	if createdFuncMain {
		w.Write("\n}\n")
	}

	if w.Error() != nil {
		err = w.Error()
		return
	}

	// Close file.
	err = f.Close()
	if err != nil {
		return cursorInFile, errors.Wrapf(err, "Failed to close %q", filePath)
	}
	f = nil
	return
}

func (s *State) createMainFileFromDecls(decls *Declarations, mainDecl *Function) (cursor Cursor, err error) {
	var f *os.File
	f, err = os.Create(s.MainPath())
	if err != nil {
		return
	}
	cursor, err = s.createMainContentsFromDecls(f, decls, mainDecl)
	err2 := f.Close()
	if err != nil {
		err = errors.Wrapf(err, "creating main.go")
		return
	}
	err = err2
	if err != nil {
		err = errors.Wrapf(err, "closing main.go")
		return
	}
	return
}

func (s *State) createMainContentsFromDecls(writer io.Writer, decls *Declarations, mainDecl *Function) (cursor Cursor, err error) {
	cursor = NoCursor
	w := NewWriterWithCursor(writer)
	w.Writef("package main\n\n")
	if err != nil {
		return
	}

	mergeCursorAndReportError := func(w *WriterWithCursor, cursorInFile Cursor, name string) bool {
		if w.Error() != nil {
			err = errors.WithMessagef(err, "in block %q", name)
			return true
		}
		if cursorInFile.HasCursor() {
			cursor = cursorInFile
		}
		return false
	}
	if mergeCursorAndReportError(w, decls.RenderImports(w), "imports") {
		return
	}
	if mergeCursorAndReportError(w, decls.RenderTypes(w), "types") {
		return
	}
	if mergeCursorAndReportError(w, decls.RenderConstants(w), "constants") {
		return
	}
	if mergeCursorAndReportError(w, decls.RenderVariables(w), "variables") {
		return
	}
	if mergeCursorAndReportError(w, decls.RenderFunctions(w), "functions") {
		return
	}

	if mainDecl != nil {
		w.Writef("\n")
		if mainDecl.HasCursor() {
			cursor = mainDecl.Cursor
			cursor.Line += w.Line
			//log.Printf("Cursor in \"main\": %v", cursor)
		}
		w.Writef("%s\n", mainDecl.Definition)
	}
	return
}

var (
	ParseError = fmt.Errorf("failed to parse cell contents")
	CursorLost = fmt.Errorf("cursor position not rendered in main.go")
)

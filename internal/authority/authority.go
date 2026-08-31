// Package authority holds the inventory of role-authority checks to the code
// it describes.
//
// The inventory is `docs/authority-inventory.md`: one row per place the harness
// enforces role authority, naming the file, the declaration, the role it binds,
// and what it refuses. This package reads that document and fails when the two
// have come apart — a listed check that moved or was renamed, and an
// authorization site the document lists nowhere.
//
// Where the document decides and this only reports: adding a row lists a check
// and removing one unlists it, neither of which is a change to any code here.
// What this package supplies is the sweep the second table is held against, and
// the sweep is a floor rather than a fence. No deterministic rule tells an
// authorization site from an ordinary refusal, so it looks for three things it
// can recognize — a function that names a role and constructs a refusal, a
// declared name carrying the authority vocabulary, and the three boundaries that
// bind a role without naming one — and a check outside all three is caught by a
// reviewer instead. That is the same bargain `internal/terms` makes with a word
// somebody coined this morning, and for the same reason.
package authority

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InventoryPath is where the inventory lives, repository-relative. It sits
// outside the artifact homes deliberately: it is a reference every role reads
// and no role owns as an artifact, and putting it inside one would make each row
// an amendment to somebody's document.
const InventoryPath = "docs/authority-inventory.md"

// InventoryHeading and ExemptionsHeading are the two headings whose tables are
// read. They are read from under a heading rather than from every table in the
// file so that prose above them can carry an example table without listing
// anything.
const (
	InventoryHeading  = "## The inventory"
	ExemptionsHeading = "## Not an authority check"
)

// Trees are the source trees the sweep reads. Everything the harness runs is in
// one of them, and a tree added later is a tree this has to be told about, which
// is a change somebody makes on purpose.
var Trees = []string{"internal", "cmd"}

// Stems are the declared names the sweep recognizes.
//
// `authoriz` and `authorit` are the vocabulary itself: `Authorize`,
// `authorities`, `AuthorityError`, `UnauthorizedRevisions`. The other three are
// the boundaries that bind a role without ever naming one — the protected-path
// gate, the independence a promotion rests on, and the leases that admit one
// actor at a time. `lease` is deliberately not matched inside `release`, which
// is a different word this repository uses constantly.
var Stems = []string{"authoriz", "authorit", "protect", "independen", "lease"}

// Entry is one row of the inventory: a check, whose authority it enforces, where
// it lives, and what it refuses.
type Entry struct {
	Check       string `json:"check"`
	Binds       string `json:"binds"`
	File        string `json:"file"`
	Declaration string `json:"declaration"`
	Refuses     string `json:"refuses"`
	// Line is where the row is in the inventory, so a problem sends somebody to
	// the row rather than to the document.
	Line int `json:"line"`
}

// Exemption is one row of the second table: something the sweep finds that is
// deliberately not an authority check, and why.
type Exemption struct {
	File        string `json:"file"`
	Declaration string `json:"declaration"`
	Reason      string `json:"reason"`
	Line        int    `json:"line"`
}

// Site is one place in the code the sweep recognizes as possibly enforcing role
// authority.
type Site struct {
	File        string `json:"file"`
	Declaration string `json:"declaration"`
	// Signal is why the sweep stopped here: `role` for a function that names a
	// role and constructs a refusal, or `name:<stem>` for a declared name.
	Signal string `json:"signal"`
}

// Problem is one way the inventory and the code disagree.
type Problem struct {
	Where  string `json:"where"`
	Reason string `json:"reason"`
}

func (p Problem) String() string { return p.Where + ": " + p.Reason }

// Inventory reads both tables out of the document.
func Inventory(root string) ([]Entry, []Exemption, error) {
	path := filepath.Join(root, filepath.FromSlash(InventoryPath))
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", InventoryPath, err)
	}
	lines := strings.Split(string(content), "\n")
	var entries []Entry
	for _, row := range rowsUnder(lines, InventoryHeading) {
		if len(row.cells) != 5 {
			return nil, nil, fmt.Errorf("%s:%d: a row of the inventory has %d cells and wants 5", InventoryPath, row.line, len(row.cells))
		}
		entries = append(entries, Entry{
			Check:       row.cells[0],
			Binds:       row.cells[1],
			File:        row.cells[2],
			Declaration: row.cells[3],
			Refuses:     row.cells[4],
			Line:        row.line,
		})
	}
	var exemptions []Exemption
	for _, row := range rowsUnder(lines, ExemptionsHeading) {
		if len(row.cells) != 3 {
			return nil, nil, fmt.Errorf("%s:%d: a row of the exemptions has %d cells and wants 3", InventoryPath, row.line, len(row.cells))
		}
		exemptions = append(exemptions, Exemption{
			File:        row.cells[0],
			Declaration: row.cells[1],
			Reason:      row.cells[2],
			Line:        row.line,
		})
	}
	return entries, exemptions, nil
}

// Sites is every place in the source the sweep recognizes, in a stable order.
func Sites(root string) ([]Site, error) {
	var sites []Site
	for _, tree := range Trees {
		treeRoot := filepath.Join(root, tree)
		if _, err := os.Stat(treeRoot); err != nil {
			// A tree a checkout does not carry is not a failure here: the caller
			// finding no sites at all is what says the sweep read the wrong place.
			continue
		}
		err := filepath.WalkDir(treeRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found, err := sitesIn(path, filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			sites = append(sites, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Declaration < sites[j].Declaration
	})
	return sites, nil
}

// Check reports every way the inventory and the code disagree.
func Check(root string) ([]Problem, error) {
	entries, exemptions, err := Inventory(root)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s lists no checks under %q; the inventory is being read from somewhere it is not", InventoryPath, InventoryHeading)
	}
	sites, err := Sites(root)
	if err != nil {
		return nil, err
	}

	var problems []Problem
	listed := map[string]Entry{}
	seenCheck := map[string]int{}
	declared := map[string]map[string]bool{}
	for _, entry := range entries {
		where := fmt.Sprintf("%s:%d", InventoryPath, entry.Line)
		if first, taken := seenCheck[entry.Check]; taken {
			problems = append(problems, Problem{Where: where,
				Reason: fmt.Sprintf("%q is listed again, having already been listed on line %d; one check is one row", entry.Check, first)})
		} else {
			seenCheck[entry.Check] = entry.Line
		}
		key := entry.File + " " + entry.Declaration
		if _, already := listed[key]; already {
			// Two rows may name one declaration: a function making two separate
			// refusals is two checks worth reviewing apart. Only the first is kept
			// for the site comparison, which needs the declaration and not the row.
			continue
		}
		listed[key] = entry
		names, err := declarationsIn(root, entry.File, declared)
		if err != nil {
			problems = append(problems, Problem{Where: where,
				Reason: fmt.Sprintf("%q lists %s, and %v", entry.Check, entry.File, err)})
			continue
		}
		if !names[entry.Declaration] {
			problems = append(problems, Problem{Where: where,
				Reason: fmt.Sprintf("%q names %s in %s, and that file declares no such thing; the check moved or was renamed",
					entry.Check, entry.Declaration, entry.File)})
		}
	}

	excused := map[string]Exemption{}
	for _, exemption := range exemptions {
		where := fmt.Sprintf("%s:%d", InventoryPath, exemption.Line)
		key := exemption.File + " " + exemption.Declaration
		if _, already := excused[key]; already {
			problems = append(problems, Problem{Where: where,
				Reason: fmt.Sprintf("%s in %s is excused twice", exemption.Declaration, exemption.File)})
			continue
		}
		excused[key] = exemption
		if entry, both := listed[key]; both {
			problems = append(problems, Problem{Where: where,
				Reason: fmt.Sprintf("%s in %s is excused and is also listed as %q; that is two answers to one question",
					exemption.Declaration, exemption.File, entry.Check)})
		}
	}

	swept := map[string]bool{}
	for _, site := range sites {
		key := site.File + " " + site.Declaration
		swept[key] = true
		if _, isListed := listed[key]; isListed {
			continue
		}
		if _, isExcused := excused[key]; isExcused {
			continue
		}
		problems = append(problems, Problem{
			Where: site.File,
			Reason: fmt.Sprintf("%s looks like an authorization site (%s) and %s lists it nowhere; add a row to %q, or excuse it under %q with the reason it is not one",
				site.Declaration, site.Signal, InventoryPath, InventoryHeading, ExemptionsHeading),
		})
	}
	for _, exemption := range exemptions {
		if swept[exemption.File+" "+exemption.Declaration] {
			continue
		}
		// An exemption for something the sweep no longer finds is a sentence that
		// has stopped being true, and the next person reads it as a statement about
		// code that is still there.
		problems = append(problems, Problem{
			Where:  fmt.Sprintf("%s:%d", InventoryPath, exemption.Line),
			Reason: fmt.Sprintf("%s in %s is excused and the sweep no longer finds it; remove the exemption", exemption.Declaration, exemption.File),
		})
	}
	return problems, nil
}

// tableRow is one `|`-delimited row and where it was read from.
type tableRow struct {
	cells []string
	line  int
}

// rowsUnder is the table immediately under a heading, without its header row or
// its separator. Reading stops at the next heading, so a document that grows a
// section between the two tables does not merge them.
func rowsUnder(lines []string, heading string) []tableRow {
	var rows []tableRow
	inSection := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			inSection = trimmed == heading
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := splitRow(trimmed)
		if isSeparator(cells) {
			continue
		}
		if len(rows) == 0 && len(cells) > 0 && strings.EqualFold(cells[0], "check") {
			continue
		}
		if len(rows) == 0 && len(cells) > 0 && strings.EqualFold(cells[0], "file") {
			continue
		}
		rows = append(rows, tableRow{cells: cells, line: index + 1})
	}
	return rows
}

// splitRow is the cells of one row, with the code spans a path and a declaration
// are written in taken off: the document reads them as code and the comparison
// wants the text.
func splitRow(row string) []string {
	trimmed := strings.Trim(row, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.Trim(strings.TrimSpace(part), "`"))
	}
	return cells
}

func isSeparator(cells []string) bool {
	for _, cell := range cells {
		if strings.Trim(cell, "-: ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// declarationsIn is every top-level thing a file declares, named the way the
// inventory writes it. The answers are kept, because an inventory names one file
// several times over.
func declarationsIn(root, file string, cache map[string]map[string]bool) (map[string]bool, error) {
	if names, known := cache[file]; known {
		return names, nil
	}
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return nil, fmt.Errorf("%s is not a non-test Go file; a check the code does not carry is not one anything enforces", file)
	}
	path := filepath.Join(root, filepath.FromSlash(file))
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("it cannot be read: %w", err)
	}
	names := map[string]bool{}
	for _, name := range declaredNames(parsed) {
		names[name] = true
	}
	cache[file] = names
	return names, nil
}

// sitesIn is every declaration in one file the sweep recognizes.
func sitesIn(path, relative string) ([]Site, error) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	var sites []Site
	// One declaration is one site however many signals stopped at it, and the
	// first signal is the one reported: a function that names a role and refuses
	// is described as that rather than as a name that happened to match.
	found := map[string]bool{}
	add := func(name, signal string) {
		if name == "" || found[name] {
			return
		}
		found[name] = true
		sites = append(sites, Site{File: relative, Declaration: name, Signal: signal})
	}
	for _, declaration := range parsed.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Body == nil {
			continue
		}
		name := functionName(function)
		if name == "" {
			continue
		}
		body := string(source[fileSet.Position(function.Pos()).Offset:fileSet.Position(function.End()).Offset])
		if namesARole(body) && constructsARefusal(body) {
			add(name, "role")
			continue
		}
		if stem, matched := matchStem(function.Name.Name); matched {
			add(name, "name:"+stem)
		}
	}
	for _, name := range declaredNames(parsed) {
		if strings.HasPrefix(name, "(") {
			continue
		}
		if stem, matched := matchStem(name); matched {
			add(name, "name:"+stem)
		}
	}
	return sites, nil
}

// namesARole reports whether a function's own text reaches for one of the
// harness's roles, which is what a check about role authority has to do.
func namesARole(body string) bool {
	return strings.Contains(body, "domain.AgentRole") || strings.Contains(body, "domain.Role")
}

// constructsARefusal reports whether a function's own text builds something to
// refuse with. A function that names a role and refuses nothing is reading,
// rendering, or routing, and the sweep leaves it alone.
func constructsARefusal(body string) bool {
	return strings.Contains(body, "errors.New") ||
		strings.Contains(body, "fmt.Errorf") ||
		strings.Contains(body, "AuthorityError{")
}

func matchStem(name string) (string, bool) {
	lowered := strings.ToLower(name)
	for _, stem := range Stems {
		if stem == "lease" && strings.Contains(lowered, "release") {
			continue
		}
		if strings.Contains(lowered, stem) {
			return stem, true
		}
	}
	return "", false
}

// declaredNames is every top-level name a parsed file declares: functions and
// methods the way an inventory row writes them, and the types, variables, and
// constants beside them.
func declaredNames(parsed *ast.File) []string {
	var names []string
	for _, declaration := range parsed.Decls {
		switch declared := declaration.(type) {
		case *ast.FuncDecl:
			if name := functionName(declared); name != "" {
				names = append(names, name)
			}
		case *ast.GenDecl:
			for _, specification := range declared.Specs {
				switch specified := specification.(type) {
				case *ast.TypeSpec:
					names = append(names, specified.Name.Name)
				case *ast.ValueSpec:
					for _, identifier := range specified.Names {
						names = append(names, identifier.Name)
					}
				}
			}
		}
	}
	return names
}

// functionName renders a declaration the way the inventory writes it:
// `Authorize` for a function and `(*Session).authorize` for a method. A receiver
// this cannot name — a generic one, say — is not named rather than guessed at.
func functionName(function *ast.FuncDecl) string {
	if function.Recv == nil {
		return function.Name.Name
	}
	if len(function.Recv.List) != 1 {
		return ""
	}
	receiver, named := receiverName(function.Recv.List[0].Type)
	if !named {
		return ""
	}
	return "(" + receiver + ")." + function.Name.Name
}

func receiverName(expression ast.Expr) (string, bool) {
	switch receiver := expression.(type) {
	case *ast.Ident:
		return receiver.Name, true
	case *ast.StarExpr:
		pointed, isIdent := receiver.X.(*ast.Ident)
		if !isIdent {
			return "", false
		}
		return "*" + pointed.Name, true
	default:
		return "", false
	}
}

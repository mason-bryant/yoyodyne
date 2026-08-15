package contextbundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"yoyodyne/internal/beads"
)

const defaultMaxBytes = 256 << 10

type Request struct {
	RepositoryRoot string
	WorkItem       beads.WorkItem
	References     []string
	MaxBytes       int
}

type Reference struct {
	Path    string
	Content string
}

type Bundle struct {
	Text       string
	References []Reference
	Bytes      int
}

var markdownReferencePattern = regexp.MustCompile(`[A-Za-z0-9._/-]+\.md`)

func Assemble(request Request) (Bundle, error) {
	if request.WorkItem.ID == "" {
		return Bundle{}, errors.New("work item is required")
	}
	root, err := filepath.Abs(request.RepositoryRoot)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBytes < 1 {
		return Bundle{}, errors.New("max context bytes must be greater than zero")
	}

	referencePaths := append([]string(nil), request.References...)
	referencePaths = append(referencePaths, ExtractMarkdownReferences(request.WorkItem)...)
	referencePaths = uniqueSorted(referencePaths)

	base := renderWorkItem(request.WorkItem)
	if len(base) > maxBytes {
		return Bundle{}, fmt.Errorf("work item context is %d bytes, exceeding limit %d", len(base), maxBytes)
	}
	bundle := Bundle{Bytes: len(base)}
	var output bytes.Buffer
	output.WriteString(base)

	for _, referencePath := range referencePaths {
		reference, err := readReference(root, referencePath, maxBytes-bundle.Bytes)
		if err != nil {
			return Bundle{}, err
		}
		header := fmt.Sprintf("\n## Referenced file: %s\n\n", reference.Path)
		additionBytes := len(header) + len(reference.Content)
		needsNewline := !strings.HasSuffix(reference.Content, "\n")
		if needsNewline {
			additionBytes++
		}
		if bundle.Bytes+additionBytes > maxBytes {
			return Bundle{}, fmt.Errorf("context exceeds %d bytes while adding %s", maxBytes, reference.Path)
		}
		output.WriteString(header)
		output.WriteString(reference.Content)
		if needsNewline {
			output.WriteByte('\n')
		}
		bundle.Bytes += additionBytes
		bundle.References = append(bundle.References, reference)
	}
	bundle.Text = output.String()
	bundle.Bytes = len(bundle.Text)
	return bundle, nil
}

func ExtractMarkdownReferences(item beads.WorkItem) []string {
	text := strings.Join([]string{item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
	matches := markdownReferencePattern.FindAllString(text, -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		// URL matches begin at the authority separator (for example,
		// //example.com/design.md). Absolute paths are not valid implicit
		// repository references either, so exclude both before resolution.
		if strings.HasPrefix(match, "/") {
			continue
		}
		references = append(references, match)
	}
	return uniqueSorted(references)
}

func readReference(root, referencePath string, remainingBytes int) (Reference, error) {
	clean := filepath.Clean(referencePath)
	if filepath.IsAbs(referencePath) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return Reference{}, fmt.Errorf("reference %q must be a repository-relative path", referencePath)
	}
	if strings.ToLower(filepath.Ext(clean)) != ".md" {
		return Reference{}, fmt.Errorf("reference %q must be a Markdown file", referencePath)
	}
	path := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Reference{}, fmt.Errorf("resolve reference %q: %w", referencePath, err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return Reference{}, fmt.Errorf("verify reference %q: %w", referencePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Reference{}, fmt.Errorf("reference %q resolves outside the repository", referencePath)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Reference{}, fmt.Errorf("stat reference %q: %w", referencePath, err)
	}
	if !info.Mode().IsRegular() {
		return Reference{}, fmt.Errorf("reference %q is not a regular file", referencePath)
	}
	if info.Size() > int64(remainingBytes) {
		return Reference{}, fmt.Errorf("reference %q exceeds remaining context limit of %d bytes", referencePath, remainingBytes)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return Reference{}, fmt.Errorf("open reference %q: %w", referencePath, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(remainingBytes)+1))
	if err != nil {
		return Reference{}, fmt.Errorf("read reference %q: %w", referencePath, err)
	}
	if len(data) > remainingBytes {
		return Reference{}, fmt.Errorf("reference %q exceeds remaining context limit of %d bytes", referencePath, remainingBytes)
	}
	return Reference{Path: filepath.ToSlash(relative), Content: string(data)}, nil
}

func renderWorkItem(item beads.WorkItem) string {
	return fmt.Sprintf(`# Assigned work item

ID: %s
Title: %s
Status: %s

## Description

%s

## Design guidance

%s

## Acceptance criteria

%s
`, item.ID, item.Title, item.Status, emptyFallback(item.Description), emptyFallback(item.Design), emptyFallback(item.AcceptanceCriteria))
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not provided."
	}
	return value
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

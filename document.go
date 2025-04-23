package main

import (
	"regexp"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type document struct {
	path     string
	contents []byte
	cache    []byte
}

var linkRE = regexp.MustCompile(`(\[[^]]+\]\(\.\/[^)]+?)\.md(\))`)

func newDocument(path string, contents []byte) (*document, error) {
	contents = []byte(linkRE.ReplaceAllString(string(contents), `$1$2`))
	return &document{path: path, contents: contents}, nil
}

func (d *document) Render() ([]byte, error) {
	if d.cache != nil {
		return d.cache, nil
	}

	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(d.contents)

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	d.cache = markdown.Render(doc, renderer)
	return d.cache, nil
}

package main

import (
	"bytes"
	"html/template"
	"net/http"
	"regexp"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

const wrapper = `
<!DOCTYPE html>
<html>
	<head>
		<title>thoughts</title>
		<style type="text/css">
			body {
				font-family: monospace;
			}
			.content {
				margin: 0 auto;
				width: 800px;
				border-left: 1px solid #eee;
				border-right: 1px solid #eee;
				padding: 20px;
			}
		</style>
	</head>
	<body>
		<div class="content">
			{{.}}
		</div>
	</body>
</html>
`

type document struct {
	path     string
	contents []byte
	cache    []byte
	t        *template.Template
}

var linkRE = regexp.MustCompile(`(\[[^]]+\]\(\.\/[^)]+?)\.md(\))`)

func newDocument(path string, contents []byte) (*document, error) {
	t, err := template.New("wrapper").Parse(wrapper)
	if err != nil {
		return nil, err
	}

	cleanedContent := linkRE.ReplaceAllString(string(contents), `$1$2`)

	return &document{path: path, contents: []byte(cleanedContent), t: t}, nil
}

func (d *document) Serve(w http.ResponseWriter, r *http.Request) {
	if d.cache != nil {
		w.Write(d.cache)
		return
	}

	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(d.contents)

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	output, err := d.render(markdown.Render(doc, renderer))
	if err != nil {
		http.Error(w, "failed to render document", http.StatusInternalServerError)
		return
	}

	d.cache = output
	w.Write(d.cache)
}

func (d *document) render(contents []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := d.t.Execute(&buf, template.HTML(contents)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

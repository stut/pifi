package captiveportal

import (
	"embed"
	_ "embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
)

//go:embed templates
var embeddedTemplates embed.FS

type Templates struct {
	disableCache bool
	// files is where templates are read from: templates_dir if configured, otherwise the embedded set. It is kept
	// so that uncached renders re-read from the same place the cached ones were parsed from.
	files           fs.FS
	parsedTemplates map[string]*template.Template
}

// parseTemplate parses a template and includes the layout
//
// TODO: Extend this to allow other templates to be included
func parseTemplate(files fs.FS, file string) (*template.Template, error) {
	return template.New(file).ParseFS(files, "layout.html", file)
}

// getTemplates gets the templates from the given directory, or the embedded templates if none is given
//
// Every template is parsed here even when caching is disabled, so a bad directory or a broken template is reported
// at startup rather than on the first request.
func getTemplates(templateDir string, withCache bool) (*Templates, error) {
	files, err := fs.Sub(embeddedTemplates, "templates")
	if err != nil {
		return nil, err
	}
	if templateDir != "" {
		files = os.DirFS(templateDir)
	}

	res := &Templates{
		disableCache:    !withCache,
		files:           files,
		parsedTemplates: make(map[string]*template.Template),
	}

	for _, name := range []string{"configure-wifi.html", "connecting.html"} {
		if res.parsedTemplates[name], err = parseTemplate(files, name); err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", name, err)
		}
	}

	return res, nil
}

// Render renders a template with the given data to the given writer
func (tpl *Templates) Render(wr http.ResponseWriter, name string, data interface{}) {
	requestedTemplate := tpl.parsedTemplates[name]
	if tpl.disableCache || requestedTemplate == nil {
		var err error
		if requestedTemplate, err = parseTemplate(tpl.files, name); err != nil {
			http.Error(wr, "Error parsing template: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := requestedTemplate.ExecuteTemplate(wr, "layout.html", data); err != nil {
		http.Error(wr, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
	}
}

package helpers

import (
	"net/http"
	"os"
	"text/template"
	"time"
)

func dict(values ...interface{}) map[string]interface{} {
	if len(values)%2 != 0 {
		panic("invalid dict call")
	}
	dict := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key := values[i].(string)
		value := values[i+1]
		dict[key] = value
	}
	return dict
}

type BaseTemplateData struct {
	SiteName string
	Version  string
}

func (data *BaseTemplateData) Init(r *http.Request) {

	data.SiteName = os.Getenv("SITE_NAME")

	version := os.Getenv("VERSION")
	if version == "" {
		version = time.Now().Format("20060102150405")
	}
	data.Version = version

}

// ParseFullPage parses the templates and returns a template object
func ParseFullPage(files ...string) (*template.Template, error) {
	var tmpl *template.Template

	allFiles := append([]string{
		"templates/base.html",
		"templates/_header.html",
		"templates/_components.html",
	}, files...)

	tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"dict": dict,
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
	}).ParseFiles(allFiles...))

	return tmpl, nil
}
